import { spawn, type ChildProcess } from 'node:child_process';
import * as fs from 'node:fs';
import * as net from 'node:net';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

import { backendEnv, createIsolatedLayout } from '../../fixtures/seedRoot.js';

// A worker backend must survive a port another process already holds.
//
// The gate that blocked the 1.0.302 release lost exactly here: the worker
// fixture probed a candidate port by binding it and closing it, then asked
// erun-app to bind that same port later, at spawn. Anything that took the port
// in between won, and the worker died before any of its specs ran:
//
//   erun-app headless: listening on http://127.0.0.1:34126/
//   listen tcp 127.0.0.1:34126: bind: address already in use
//   Error: worker backend exited before becoming ready (code 1, signal null)
//
// The window between "somebody checked" and "erun-app bound" is the defect, so
// the contract this pins is the one that closes it: erun-app's own bind is the
// only check, a port it cannot have is not fatal, and the harness uses the
// address erun-app announces rather than the one it asked for. This spec is
// the reproduction of the reported failure — a competing holder of the
// requested port, which is the state that lost the race — driven through the
// same binary and the same announcement the fixture reads.
//
// It lives under tests/harness/ rather than tests/areas/ deliberately: it
// belongs to no area and drives no desktop flow, so it takes the base `test`
// (never fixtures/erunApp.ts) and boots no worker backend of its own. It also
// deliberately does not import the fixture's own readiness helper: the point is
// that this exact spec runs unchanged against the pre-fix and post-fix code, so
// it observes the child process itself.

const SUITE_DIR = path.join(__dirname, '..', '..');
const ERUN_UI_DIR = path.join(SUITE_DIR, '..');
const BIN_PATH = path.join(
  ERUN_UI_DIR,
  process.platform === 'win32' ? 'bin/erun-app.exe' : 'bin/erun-app',
);

// The address erun-app announces it is serving on. Matched by the fixture too
// (fixtures/workerBackend.ts) — the announcement is the handshake between the
// two, so its shape is load-bearing on both sides.
const ANNOUNCED_ADDRESS = /erun-app headless: listening on http:\/\/127\.0\.0\.1:(\d+)\//;

// holdPort binds 127.0.0.1:0 and keeps the listener, so the kernel picks a port
// and nothing else can take it: a competing holder that is genuinely holding,
// rather than a probe that released it again. Connections are refused so an
// occupied port can never be mistaken for a ready backend.
async function holdPort(): Promise<{ port: number; release: () => Promise<void> }> {
  const server = net.createServer((socket) => socket.destroy());
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address() as net.AddressInfo;
  return {
    port,
    release: () =>
      new Promise<void>((resolve) => {
        // Every accepted connection is already destroyed; closeAllConnections
        // only exists from Node 18.2 on, so it stays optional.
        (server as { closeAllConnections?: () => void }).closeAllConnections?.();
        server.close(() => {
          resolve();
        });
      }),
  };
}

// awaitBackendReady fails fast if the child exits before answering, so a backend
// that never comes up reports its own output instead of a generic timeout.
async function awaitBackendReady(
  child: ChildProcess,
  timeoutMs: number,
  output: () => string,
): Promise<{ baseURL: string; port: number }> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(
        `backend exited before becoming ready ` +
          `(code ${String(child.exitCode)}, signal ${String(child.signalCode)}):\n${output()}`,
      );
    }
    const announced = ANNOUNCED_ADDRESS.exec(output());
    if (announced) {
      const url = `http://127.0.0.1:${announced[1]}/`;
      try {
        const res = await fetch(url);
        if (res.status < 500) {
          return { baseURL: url, port: Number(announced[1]) };
        }
      } catch {
        // Not accepting connections — keep polling.
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`backend never became ready within ${timeoutMs}ms:\n${output()}`);
}

// isolatedBackendEnv stages a throwaway config root for the child and returns
// the env that points it there. seedRoot reads ERUN_PLAYWRIGHT_HOME, so the
// override is scoped to the staging call and the worker's own root is put back
// afterwards — this spec shares its process with the rest of the worker.
function isolatedBackendEnv(root: string): Record<string, string> {
  const previousRoot = process.env.ERUN_PLAYWRIGHT_HOME;
  process.env.ERUN_PLAYWRIGHT_HOME = root;
  try {
    createIsolatedLayout();
    return backendEnv();
  } finally {
    if (previousRoot === undefined) {
      delete process.env.ERUN_PLAYWRIGHT_HOME;
    } else {
      process.env.ERUN_PLAYWRIGHT_HOME = previousRoot;
    }
  }
}

async function stopBackend(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) {
    return;
  }
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      resolve();
    }, 5_000);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

test('a worker backend survives a competing holder of the port it was asked for', async () => {
  test.setTimeout(60_000);
  expect(
    fs.existsSync(BIN_PATH),
    `${BIN_PATH} is missing — build the desktop first (./run.sh --build)`,
  ).toBe(true);

  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-playwright-home.port-conflict-'));
  const env = isolatedBackendEnv(root);
  const holder = await holdPort();
  const output: Buffer[] = [];
  try {
    const child = spawn(BIN_PATH, ['--headless', '--port', String(holder.port)], {
      cwd: ERUN_UI_DIR,
      env: { ...process.env, ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    child.stdout?.on('data', (chunk: Buffer) => output.push(chunk));
    child.stderr?.on('data', (chunk: Buffer) => output.push(chunk));
    try {
      const backend = await awaitBackendReady(child, 30_000, () =>
        Buffer.concat(output).toString('utf8'),
      );

      // The holder never released the requested port, so a backend that came up
      // at all came up somewhere else and said so.
      expect(backend.port).not.toBe(holder.port);
      expect((await fetch(backend.baseURL)).status).toBeLessThan(500);
    } finally {
      await stopBackend(child);
    }
  } finally {
    await holder.release();
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  }
});

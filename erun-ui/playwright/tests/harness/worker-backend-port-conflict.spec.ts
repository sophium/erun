import { spawn, type ChildProcess } from 'node:child_process';
import * as fs from 'node:fs';
import * as net from 'node:net';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

import { backendEnv, createIsolatedLayout } from '../../fixtures/seedRoot.js';

// A worker backend must survive a port another process already holds: erun-app's
// own bind is the only check, and the harness uses the address it announces.
// The child is observed directly so this runs unchanged against any fixture.

const SUITE_DIR = path.join(__dirname, '..', '..');
const ERUN_UI_DIR = path.join(SUITE_DIR, '..');
const BIN_PATH = path.join(
  ERUN_UI_DIR,
  process.platform === 'win32' ? 'bin/erun-app.exe' : 'bin/erun-app',
);

// The announcement is the handshake with fixtures/workerBackend.ts.
const ANNOUNCED_ADDRESS = /erun-app headless: listening on http:\/\/127\.0\.0\.1:(\d+)\//;

// holdPort keeps a listener on a kernel-chosen port; connections are refused so
// it cannot pass as a ready backend.
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
        // closeAllConnections exists only from Node 18.2.
        (server as { closeAllConnections?: () => void }).closeAllConnections?.();
        server.close(() => {
          resolve();
        });
      }),
  };
}

// awaitBackendReady fails fast with the child's output if it exits early.
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

// isolatedBackendEnv stages a throwaway config root, restoring the worker's
// ERUN_PLAYWRIGHT_HOME afterwards.
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
    `${BIN_PATH} is missing — build the desktop first (../build.sh)`,
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

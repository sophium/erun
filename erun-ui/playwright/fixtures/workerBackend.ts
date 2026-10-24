import { test as base } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import {
  backendEnv,
  createIsolatedLayout,
  e2eK3dEnabled,
  isolatedRoot,
  removeWorkerRoot,
  seedBaseline,
  setWorkerRoot,
} from './seedRoot.js';

const isWindows = process.platform === 'win32';

// Shared with playwright.config.ts; each worker prefers a port above this base.
const BASE_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

// Kept in lockstep with playwright.config.ts's webServer command.
const ERUN_UI_DIR = path.join(__dirname, '..', '..');
const BIN_PATH = path.join(ERUN_UI_DIR, isWindows ? 'bin/erun-app.exe' : 'bin/erun-app');

// The requested port is only a preference: probing first races other processes, so the
// backend's own announcement (main.go listenHeadless) is the source of truth.
const ANNOUNCED_ADDRESS = /erun-app headless: listening on http:\/\/127\.0\.0\.1:(\d+)\//;

// awaitBackendReady fails fast if the child exits, so a crash loop is not hidden as a timeout.
async function awaitBackendReady(
  child: ChildProcess,
  timeoutMs: number,
  output: () => string,
): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  let announcedURL: string | undefined;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(
        `worker backend exited before becoming ready ` +
          `(code ${String(child.exitCode)}, signal ${String(child.signalCode)}):\n${output()}`,
      );
    }
    announcedURL ??= announcedBackendURL(output());
    if (announcedURL !== undefined) {
      try {
        const res = await fetch(announcedURL);
        if (res.status < 500) {
          return announcedURL;
        }
      } catch {
        // Not accepting connections yet.
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  if (announcedURL !== undefined) {
    throw new Error(
      `worker backend never became ready at ${announcedURL} within ${timeoutMs}ms:\n${output()}`,
    );
  }
  throw new Error(
    `worker backend never announced a listening address within ${timeoutMs}ms:\n${output()}`,
  );
}

function announcedBackendURL(output: string): string | undefined {
  const announced = ANNOUNCED_ADDRESS.exec(output);
  return announced ? `http://127.0.0.1:${String(announced[1])}` : undefined;
}

// stopBackend sends SIGTERM and force-kills after a bound.
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

type WorkerFixtures = {
  workerBaseURL: string;
};

// One headless backend per worker, each with its own root. e2e-k3d mode (workers: 1) reuses
// the shared backend playwright.config.ts starts.
export const test = base.extend<object, WorkerFixtures>({
  workerBaseURL: [
    async ({}, use, workerInfo) => {
      if (e2eK3dEnabled()) {
        await use(`http://127.0.0.1:${BASE_PORT}`);
        return;
      }

      // A stable preferred port only eases finding a stray backend by hand.
      const preferredPort = BASE_PORT + workerInfo.parallelIndex;
      const root = path.join(isolatedRoot(), `worker-${workerInfo.parallelIndex}`);
      fs.mkdirSync(root, { recursive: true });
      setWorkerRoot(root);
      createIsolatedLayout();
      seedBaseline();

      const output: Buffer[] = [];
      const child = spawn(BIN_PATH, ['--headless', '--port', String(preferredPort)], {
        cwd: ERUN_UI_DIR,
        env: { ...process.env, ...backendEnv() },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      child.stdout?.on('data', (chunk: Buffer) => output.push(chunk));
      child.stderr?.on('data', (chunk: Buffer) => output.push(chunk));

      let baseURL: string;
      try {
        baseURL = await awaitBackendReady(child, 30_000, () =>
          Buffer.concat(output).toString('utf8'),
        );
      } catch (err) {
        await stopBackend(child);
        removeWorkerRoot(root);
        throw err;
      }

      await use(baseURL);

      await stopBackend(child);
      // Reap after stopBackend: a stub killed under a live backend can be respawned past the sweep.
      removeWorkerRoot(root);
    },
    { scope: 'worker' },
  ],
  baseURL: async ({ workerBaseURL }, use) => {
    await use(workerBaseURL);
  },
});

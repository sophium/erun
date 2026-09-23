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
import { reapStubProcesses } from './stubProcesses.js';

const isWindows = process.platform === 'win32';

// Shared with run.sh and playwright.config.ts so an overridden port reaches
// every layer; each worker then prefers its own port above this base.
const BASE_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

// erun-ui dir, one level up from this playwright dir. Windows produces
// bin/erun-app.exe; POSIX produces an extensionless bin/erun-app (see
// playwright.config.ts's webServer command, kept in lockstep for the e2e-k3d
// branch below).
const ERUN_UI_DIR = path.join(__dirname, '..', '..');
const BIN_PATH = path.join(ERUN_UI_DIR, isWindows ? 'bin/erun-app.exe' : 'bin/erun-app');

// The port a worker asks its backend for is a preference, never an assumption.
// The backend announces the address it actually bound, on startup, and this
// fixture serves whatever it announced — so a port another process already
// holds (a leftover backend from a run that died partway, or any host process
// that reached it first) costs the worker a different port, not the whole run:
//
//   erun-app headless: listening on http://127.0.0.1:34126/
//   listen tcp 127.0.0.1:34126: bind: address already in use
//   Error: worker backend exited before becoming ready
//
// is what the worker used to die with. Probing for a port first cannot fix it:
// the probe released the port before the backend ever bound it, and whatever
// took it in between won. Only the backend's own bind can, so the address is
// read back from the announcement (erun-ui/main.go listenHeadless) rather than
// decided here.
const ANNOUNCED_ADDRESS = /erun-app headless: listening on http:\/\/127\.0\.0\.1:(\d+)\//;

// awaitBackendReady waits for the backend to announce an address and then polls
// that address' HTTP root the way Playwright's own webServer readiness check
// does, failing fast if the child exits before ever answering — a
// hang-until-timeout would otherwise hide a crash loop behind a generic "never
// became ready" error.
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
        // Not accepting connections yet — keep polling.
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

// announcedBackendURL reads the address the backend says it is serving on, or
// undefined while it has not said so yet. Anchored on the announced port rather
// than a caller-supplied one on purpose: a caller that guesses here is the
// defect this file exists to avoid.
function announcedBackendURL(output: string): string | undefined {
  const announced = ANNOUNCED_ADDRESS.exec(output);
  return announced ? `http://127.0.0.1:${String(announced[1])}` : undefined;
}

// stopBackend sends a graceful shutdown (main.go's runHeadless turns SIGTERM
// into an HTTP server Shutdown) and force-kills only if it does not exit in
// time, so a worker teardown never hangs the whole run on a stuck child.
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

// One headless backend per Playwright worker, replacing the single shared
// singleton the old config's webServer block started. Each worker is a
// separate OS process, so its own subdirectory + setWorkerRoot (seedRoot.ts)
// can never collide with another worker's root or config, and the port is
// offset by the worker's parallelIndex (stable across restarts, bounded by
// `workers`).
//
// The opt-in e2e-k3d mode keeps the original single shared backend + single
// real cluster instead: it is always workers: 1, and
// playwright.config.ts still declares the webServer block and global-setup
// still seeds the one root before this fixture ever runs, so this branch just
// points at that already-running backend rather than spawning a second one.
export const test = base.extend<object, WorkerFixtures>({
  workerBaseURL: [
    async ({}, use, workerInfo) => {
      if (e2eK3dEnabled()) {
        await use(`http://127.0.0.1:${BASE_PORT}`);
        return;
      }

      // A stable per-worker port keeps a stray backend easy to find by hand.
      // Nothing depends on getting it: awaitBackendReady serves the address the
      // backend announces, whatever that turns out to be.
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
      // Backstop for the stub processes the last spec in this worker opened:
      // they are not children of the backend, so stopping it leaves them parked.
      // After stopBackend, never before — a stub killed under a live backend can
      // be respawned by the reconnect path and end up outside this sweep.
      reapStubProcesses();
      removeWorkerRoot(root);
    },
    { scope: 'worker' },
  ],
  baseURL: async ({ workerBaseURL }, use) => {
    await use(workerBaseURL);
  },
});

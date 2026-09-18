import { test as base } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import * as net from 'node:net';
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

// Shared with run.sh and playwright.config.ts so an overridden port reaches
// every layer; each worker then claims its own port above this base.
const BASE_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

// erun-ui dir, one level up from this playwright dir. Windows produces
// bin/erun-app.exe; POSIX produces an extensionless bin/erun-app (see
// playwright.config.ts's webServer command, kept in lockstep for the e2e-k3d
// branch below).
const ERUN_UI_DIR = path.join(__dirname, '..', '..');
const BIN_PATH = path.join(ERUN_UI_DIR, isWindows ? 'bin/erun-app.exe' : 'bin/erun-app');

// claimFreePort finds a port this worker can actually bind, rather than
// assuming its assigned one is free. A gate that fails partway can leave its
// erun-app backends running, and the next run then dies on every test in the
// affected worker with a message that names accessibility specs rather than
// the real cause (erun#2362):
//
//   listen tcp 127.0.0.1:34124: bind: address already in use
//   Error: worker backend exited before becoming ready
//
// Candidates step by the worker count so each worker only ever tries ports
// congruent to its own index: two workers racing can never land on the same
// one, so a retry can only collide with a foreign process, never with a
// sibling.
async function claimFreePort(start: number, stride: number, attempts = 20): Promise<number> {
  for (let i = 0; i < attempts; i++) {
    const candidate = start + i * stride;
    const free = await new Promise<boolean>((resolve) => {
      const probe = net.createServer();
      probe.once('error', () => resolve(false));
      probe.once('listening', () => probe.close(() => resolve(true)));
      probe.listen(candidate, '127.0.0.1');
    });
    if (free) {
      return candidate;
    }
  }
  throw new Error(
    `no free port for this worker: tried ${String(attempts)} candidates from ${String(start)} ` +
      `stepping by ${String(stride)}. A previous run probably left erun-app backends behind.`,
  );
}

// waitForBackendReady polls the backend's HTTP root the same way Playwright's
// own webServer readiness check does, but also fails fast if the child exits
// before ever answering — a hang-until-timeout would otherwise hide a crash
// loop behind a generic "never became ready" error.
async function waitForBackendReady(
  child: ChildProcess,
  url: string,
  timeoutMs: number,
  output: () => string,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(
        `worker backend exited before becoming ready ` +
          `(code ${String(child.exitCode)}, signal ${String(child.signalCode)}):\n${output()}`,
      );
    }
    try {
      const res = await fetch(url);
      if (res.status < 500) {
        return;
      }
    } catch {
      // Not accepting connections yet — keep polling.
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `worker backend never became ready at ${url} within ${timeoutMs}ms:\n${output()}`,
  );
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

      const port = await claimFreePort(
        BASE_PORT + workerInfo.parallelIndex,
        Math.max(1, workerInfo.config.workers),
      );
      const root = path.join(isolatedRoot(), `worker-${workerInfo.parallelIndex}`);
      fs.mkdirSync(root, { recursive: true });
      setWorkerRoot(root);
      createIsolatedLayout();
      seedBaseline();

      const output: Buffer[] = [];
      const child = spawn(BIN_PATH, ['--headless', '--port', String(port)], {
        cwd: ERUN_UI_DIR,
        env: { ...process.env, ...backendEnv() },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      child.stdout?.on('data', (chunk: Buffer) => output.push(chunk));
      child.stderr?.on('data', (chunk: Buffer) => output.push(chunk));

      const baseURL = `http://127.0.0.1:${port}`;
      try {
        await waitForBackendReady(child, `${baseURL}/`, 30_000, () =>
          Buffer.concat(output).toString('utf8'),
        );
      } catch (err) {
        await stopBackend(child);
        removeWorkerRoot(root);
        throw err;
      }

      await use(baseURL);

      await stopBackend(child);
      removeWorkerRoot(root);
    },
    { scope: 'worker' },
  ],
  baseURL: async ({ workerBaseURL }, use) => {
    await use(workerBaseURL);
  },
});

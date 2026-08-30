import * as fs from 'node:fs';
import * as os from 'node:os';

import { defineConfig, devices } from '@playwright/test';
import { backendEnv, e2eK3dEnabled, isolatedRoot } from './fixtures/seedRoot.js';

// run.sh and this config share ERUN_PLAYWRIGHT_PORT so an overridden port
// reaches both; the default stays clear of wails dev's 34115. Each worker in
// the default mode claims BASE_PORT + its own parallelIndex (see
// fixtures/workerBackend.ts) instead of every spec sharing this one port.
const HEADLESS_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

const E2E_K3D = e2eK3dEnabled();

// Resolve (and, without run.sh, create) the isolated root at config-load
// time, before workers fork, so every process in the run agrees on the same
// starting point. In the default parallel mode this is the PARENT directory
// each worker mints its own subdirectory under (fixtures/workerBackend.ts —
// one headless backend per worker, replacing the old shared singleton). In
// the opt-in e2e-k3d mode (workers: 1, one real cluster for the whole run) it
// stays the flat single root the one shared backend below boots against.
isolatedRoot();

// Each worker in the default mode is a full desktop backend plus a headless
// Chromium instance, so the ceiling is memory, not cores. Measured ~550MiB of
// growth per worker on the reference agent pod; WORKER_BUDGET_MIB leaves room
// for Chromium alongside each backend.
//
// Derived rather than fixed: a constant tuned to one pod silently oversubscribes
// a smaller one. os.availableParallelism() honours the cgroup CPU quota (unlike
// os.cpus().length, which reports the host's cores), and the memory ceiling is
// read from the cgroup when present so a container limit is respected rather
// than the node's total RAM. scripts/parallel-gate.sh's `width` mode is the
// analogous derivation for the Makefile's gate widths (#1702) -- it can't
// share this function across languages, but reads the same cgroup files with
// the same v2-then-v1-then-unlimited fallback order and the same
// Number.MAX_SAFE_INTEGER treatment of v1's huge "unlimited" sentinel below.
const WORKER_BUDGET_MIB = 900;

function cgroupMemoryLimitMib(): number | null {
  for (const p of ['/sys/fs/cgroup/memory.max', '/sys/fs/cgroup/memory/memory.limit_in_bytes']) {
    try {
      const raw = fs.readFileSync(p, 'utf8').trim();
      if (raw === 'max') continue;
      const bytes = Number(raw);
      if (Number.isFinite(bytes) && bytes > 0 && bytes < Number.MAX_SAFE_INTEGER) {
        return Math.floor(bytes / (1024 * 1024));
      }
    } catch {
      // Not a cgroup v2/v1 host (macOS, Windows); fall through to os.totalmem.
    }
  }
  return null;
}

function defaultWorkers(): number {
  const override = Number(process.env.ERUN_PLAYWRIGHT_WORKERS);
  if (Number.isInteger(override) && override > 0) {
    return override;
  }
  // Two cores per worker, not one: a worker is a Go backend *and* a headless
  // Chromium, and they compete. Measured on a 4-core environment, 3 workers
  // (cores - 1) failed two specs on timeouts while 2 workers passed clean; the
  // 12-core environment runs 6 without a contention failure. Both land on
  // cores/2, so that is the rule rather than a number picked per machine.
  const byCpu = Math.max(1, Math.floor(os.availableParallelism() / 2));
  const limitMib = cgroupMemoryLimitMib() ?? Math.floor(os.totalmem() / (1024 * 1024));
  const byMemory = Math.max(1, Math.floor((limitMib * 0.6) / WORKER_BUDGET_MIB));
  return Math.max(1, Math.min(byCpu, byMemory, 6));
}

const DEFAULT_WORKERS = defaultWorkers();

export default defineConfig({
  testDir: './tests',
  // The k3d e2e specs need a real cluster and the un-stubbed backend, so they
  // must never run in the default inert mode. Excluding the dir here (not
  // per-spec test.skip) keeps the default suite from ever collecting them.
  testIgnore: E2E_K3D ? [] : ['**/tests/e2e/**'],
  globalSetup: './global-setup',
  globalTeardown: './global-teardown',
  // The e2e-k3d mode drives one real cluster and one shared backend, so it
  // stays serial exactly as before parallelism existed for the default mode.
  fullyParallel: !E2E_K3D,
  workers: E2E_K3D ? 1 : DEFAULT_WORKERS,
  forbidOnly: !!process.env.CI,
  // No retries: a spec that only passes on a retry is flaky, and flakiness is a
  // determinism defect to fix, never to mask (see AGENTS.md "No flaky tests").
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  // Windows runs the heavier full Chromium build (see the chromium project) and
  // its ConPTY-backed sessions are slower than a unix pty, so under the full
  // suite's shared-backend load a borderline spec can exceed the POSIX-tuned
  // budgets. Give the auto-retrying assertions more room on Windows to absorb
  // the loaded machine (AGENTS.md: raise the wait, never lower it, and never add
  // retries) — the POSIX budgets are unchanged.
  timeout: process.platform === 'win32' ? 90_000 : 30_000,
  expect: {
    timeout: process.platform === 'win32' ? 20_000 : 10_000,
  },
  use: {
    // baseURL is not set here: fixtures/workerBackend.ts overrides the
    // baseURL fixture per worker (each worker's own port in the default
    // mode; the one shared HEADLESS_PORT below in e2e-k3d mode).
    headless: true,
    // The env-init and manage dialogs render all sections inline and can
    // be taller than a normal 900px viewport. Bump the height so footer
    // buttons stay reachable without artificial scrolling.
    viewport: { width: 1440, height: 1200 },
    // Retries are off, so capture the trace on every failure (not just on a
    // retry that never happens) to keep failures debuggable via `yarn report`.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // devices['Desktop Chrome'] resets the viewport, so re-apply the tall
        // one (see top-level use.viewport) to keep dialog footer buttons reachable.
        viewport: { width: 1440, height: 1200 },
        // On Windows, run the full Chromium build instead of the default minimal
        // chrome-headless-shell: an endpoint-security agent intermittently
        // access-violation-crashes the headless-shell process mid-suite
        // (exitCode 0xC0000005), which the full build does not trigger. Left as
        // the default (headless shell) elsewhere.
        ...(process.platform === 'win32' ? { channel: 'chromium' } : {}),
      },
    },
  ],
  // The default mode has no webServer block at all: fixtures/workerBackend.ts
  // spawns one headless backend per worker instead of one shared singleton.
  // Only the opt-in e2e-k3d mode (workers: 1, one real cluster + one shared
  // backend for the whole run) still declares one here.
  webServer: E2E_K3D
    ? {
        // Windows produces bin/erun-app.exe; POSIX produces an extensionless
        // bin/erun-app. cwd is erun-ui (one level up from this playwright dir).
        command: `${process.platform === 'win32' ? '.\\bin\\erun-app.exe' : './bin/erun-app'} --headless --port ${HEADLESS_PORT}`,
        url: `http://127.0.0.1:${HEADLESS_PORT}/`,
        cwd: '..',
        // The backend must boot against this run's isolated root. A leftover
        // dev server on the same port would be pointed at a stale (or worse,
        // the developer's real) config root, so never reuse one.
        reuseExistingServer: false,
        timeout: 30_000,
        stdout: 'pipe',
        stderr: 'pipe',
        env: backendEnv(),
      }
    : undefined,
});

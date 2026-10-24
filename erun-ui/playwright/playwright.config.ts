import * as fs from 'node:fs';
import * as os from 'node:os';

import { defineConfig, devices } from '@playwright/test';
import {
  PLAYWRIGHT_REPORT_DIRNAME,
  TEST_RESULTS_DIRNAME,
  artifactPath,
  assertArtifactDirectoriesUsable,
} from './fixtures/artifacts.js';
import { backendEnv, e2eK3dEnabled, isolatedRoot } from './fixtures/seedRoot.js';

// Default stays clear of wails dev's 34115; workers prefer BASE_PORT + parallelIndex.
const HEADLESS_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

const E2E_K3D = e2eK3dEnabled();

// Fail fast on an unusable artifact directory before any worker starts.
assertArtifactDirectoriesUsable();

// Resolved before workers fork so every process agrees on the root.
isolatedRoot();

// Memory-bound: each worker is a backend plus Chromium (~550MiB measured per backend).
// Limits come from the cgroup so container quotas are respected.
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
      // Not a cgroup host; fall through to os.totalmem.
    }
  }
  return null;
}

function defaultWorkers(): number {
  const override = Number(process.env.ERUN_PLAYWRIGHT_WORKERS);
  if (Number.isInteger(override) && override > 0) {
    return override;
  }
  // A worker is a Go backend and a Chromium competing for CPU; cores/2 measured stable.
  const byCpu = Math.max(1, Math.floor(os.availableParallelism() / 2));
  const limitMib = cgroupMemoryLimitMib() ?? Math.floor(os.totalmem() / (1024 * 1024));
  const byMemory = Math.max(1, Math.floor((limitMib * 0.6) / WORKER_BUDGET_MIB));
  return Math.max(1, Math.min(byCpu, byMemory, 6));
}

const DEFAULT_WORKERS = defaultWorkers();

export default defineConfig({
  // Each mode must never collect the other's specs.
  testDir: E2E_K3D ? './tests/e2e' : './tests',
  testIgnore: E2E_K3D ? [] : ['**/tests/e2e/**'],
  globalSetup: './global-setup',
  globalTeardown: './global-teardown',
  fullyParallel: !E2E_K3D,
  workers: E2E_K3D ? 1 : DEFAULT_WORKERS,
  forbidOnly: !!process.env.CI,
  // No retries: flakiness is a defect to fix (AGENTS.md "No flaky tests").
  retries: 0,
  // Honour ERUN_PLAYWRIGHT_ARTIFACTS_DIR for the reports too.
  outputDir: artifactPath(TEST_RESULTS_DIRNAME),
  // Surfaces a browser launch death with its cause, which Playwright otherwise buries.
  reporter: [
    ['list'],
    ['./reporters/browserLaunchFailure.ts'],
    ['html', { open: 'never', outputFolder: artifactPath(PLAYWRIGHT_REPORT_DIRNAME) }],
  ],
  // Windows runs full Chromium and ConPTY, which are slower; raise waits, never add retries.
  timeout: process.platform === 'win32' ? 90_000 : 30_000,
  expect: {
    timeout: process.platform === 'win32' ? 20_000 : 10_000,
  },
  use: {
    // baseURL is set per worker by fixtures/workerBackend.ts.
    headless: true,
    // Tall enough for the inline dialogs' footer buttons.
    viewport: { width: 1440, height: 1200 },
    // Retries are off, so retain traces on every failure.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // devices['Desktop Chrome'] resets the viewport.
        viewport: { width: 1440, height: 1200 },
        // Endpoint security intermittently crashes chrome-headless-shell on Windows (0xC0000005).
        ...(process.platform === 'win32' ? { channel: 'chromium' } : {}),
      },
    },
  ],
  // The default mode spawns a backend per worker instead (fixtures/workerBackend.ts).
  webServer: E2E_K3D
    ? {
        command: `${process.platform === 'win32' ? '.\\bin\\erun-app.exe' : './bin/erun-app'} --headless --port ${HEADLESS_PORT}`,
        url: `http://127.0.0.1:${HEADLESS_PORT}/`,
        cwd: '..',
        // Never reuse a server that may point at a stale or real config root.
        reuseExistingServer: false,
        timeout: 30_000,
        stdout: 'pipe',
        stderr: 'pipe',
        env: backendEnv(),
      }
    : undefined,
});

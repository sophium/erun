import { test as base, expect } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from './seedRoot.js';

// SeededEnvironment is the handle the per-test `seededEnv` fixture yields:
// a uniquely-named inert local-agent env under the baseline tenant, created
// for this test only and removed afterwards.
export interface SeededEnvironment {
  tenant: string;
  environment: string;
}

// Test fixtures:
//
// - `app`: each test gets a fresh AppShell wired to its own Page. The
//   fixture handles boot synchronization (open + wait-for-sidebar) so spec
//   bodies can jump straight to behaviour.
// - `seededEnv`: provisions a unique throwaway env (`<spec-slug>-<rand>`
//   under the `pw` tenant) by writing the same config tree the global
//   baseline uses; the backend's fsnotify config watcher surfaces the new
//   row, and teardown removes the env again. Use it in specs that mutate
//   per-env state (open/close, tab churn, status injection) so the shared
//   baseline rows stay quiet for everyone else.
export const test = base.extend<{ app: AppShell; seededEnv: SeededEnvironment }>({
  app: async ({ page }, use) => {
    const app = new AppShell(page);
    await app.open();
    await use(app);
  },
  seededEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedEnvironment(SEED_TENANT, environment);
    // The config watcher debounces ~250 ms before emitting
    // environments-changed; wait for the row so specs start from a
    // mounted, clickable env.
    await app.sidebar
      .envRowButton(SEED_TENANT, environment)
      .waitFor({ state: 'visible', timeout: 10_000 });
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
});

export { expect };

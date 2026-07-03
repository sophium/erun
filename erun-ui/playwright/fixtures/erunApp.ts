import { test as base, expect } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from './seedRoot.js';

// Handle for a throwaway env that lives only for the duration of one test.
export interface SeededEnvironment {
  tenant: string;
  environment: string;
}

// Use the `seededEnv` fixture in specs that mutate per-env state (open/close,
// tab churn, status injection) so the shared baseline rows stay quiet for
// other specs.
export const test = base.extend<{ app: AppShell; seededEnv: SeededEnvironment }>({
  app: async ({ page }, use) => {
    const app = new AppShell(page);
    await app.open();
    await use(app);
  },
  seededEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedEnvironment(SEED_TENANT, environment);
    // The backend's fsnotify config watcher normally surfaces the new row, but
    // the very first env created right after boot can race the watcher's
    // readiness — the create/write events are missed and the row never appears
    // within the wait (a flake, ~40% when this fixture is the first env change
    // after boot). The config file is already on disk (synchronous write), and
    // the backend reads config fresh on each state refetch, so a forced reload
    // surfaces the row deterministically, independent of fsnotify timing.
    await app.reloadEnvironments();
    await app.sidebar
      .envRowButton(SEED_TENANT, environment)
      .waitFor({ state: 'visible', timeout: 15_000 });
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
});

export { expect };

import { test as base, expect } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  seedHostEnvironment,
  seedRuntimeEnvironment,
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
export const test = base.extend<{
  app: AppShell;
  seededEnv: SeededEnvironment;
  seededRuntimeEnv: SeededEnvironment;
  seededHostEnv: SeededEnvironment;
}>({
  app: async ({ page }, use) => {
    const app = new AppShell(page);
    await app.open();
    await use(app);
  },
  seededEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  // A per-test inert runtime-type env (RemoteRepo), for specs that exercise the
  // sourceless deploy path — where the Components checklist offers the published
  // platform components by reference rather than local charts.
  seededRuntimeEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedRuntimeEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  // A per-test inert host-type env (no pod, no cluster at all), for specs
  // that exercise the host badge and its no-pod-shaped-actions contract.
  seededHostEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedHostEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
});

// waitForSeededRow surfaces a freshly-written env config in the sidebar.
//
// The backend's fsnotify watcher normally does this, but it can miss the
// create/write events — most reliably right after boot, before the watcher is
// ready. A forced reload covers that, except that one reload is not
// sufficient either: the desktop's state refetch is deduplicated, so a reload
// issued while an earlier refetch is already in flight can resolve against a
// snapshot taken *before* the config was written, and nothing re-triggers.
// Re-driving the reload until the row appears converges on the observable
// condition instead of betting on one round-trip winning the race — a genuinely
// missing row still never converges, so the step still fails.
async function waitForSeededRow(app: AppShell, environment: string): Promise<void> {
  await expect(async () => {
    await app.reloadEnvironments();
    await app.sidebar
      .envRowButton(SEED_TENANT, environment)
      .waitFor({ state: 'visible', timeout: 2_000 });
  }).toPass({ timeout: 30_000 });
}

export { expect };

import { expect } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import { test as base } from './workerBackend.js';
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
    await waitForSeededRow(app, SEED_TENANT, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  // A per-test inert runtime-type env (RemoteRepo), for specs that exercise the
  // sourceless deploy path — where the Components checklist offers the published
  // platform components by reference rather than local charts.
  seededRuntimeEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedRuntimeEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, SEED_TENANT, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  // A per-test inert host-type env (no pod, no cluster at all), for specs
  // that exercise the host badge and its no-pod-shaped-actions contract.
  seededHostEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedHostEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, SEED_TENANT, environment);
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
// missing row still never converges, so the step still fails. Exported so any
// spec that seeds its own env config directly (rather than through the
// fixtures above) can key its wait to the same observable precondition
// instead of a single fixed-timeout reload+waitFor.
//
// timeoutMs defaults to the budget every other caller relies on. A spec that
// seeds an unusually large population before calling this (many environments
// and/or orchestrators in one go) makes each reload genuinely more expensive
// to resolve and render, not just slower to observe -- titlebar-whip-panel-
// layout.spec.ts's realistic-population case (erun#1748) widens it for that
// reason rather than accepting a marginal budget tuned for a single-row wait.
export async function waitForSeededRow(
  app: AppShell,
  tenant: string,
  environment: string,
  timeoutMs = 30_000,
): Promise<void> {
  await expect(async () => {
    await app.reloadEnvironments();
    await app.sidebar
      .envRowButton(tenant, environment)
      .waitFor({ state: 'visible', timeout: 2_000 });
  }).toPass({ timeout: timeoutMs });
}

export { expect };

import { expect, type Locator } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import { test as base } from './workerBackend.js';
import {
  SEED_TENANT,
  e2eK3dEnabled,
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

// resetSharedBaselineObservations clears the worker backend's cached
// Activity/Usage observations (erun-ui/environment_activity.go,
// environment_usage.go) before the app boots. Those maps live for the whole
// worker process, not one spec file, so without this a genuine observation
// sampled during an earlier spec in this worker — even a routine "not
// reachable" reading for a never-deployed seeded env, complete with its own
// real observedAt age — renders on SEED_ENV_ALPHA/SEED_ENV_BETA's hover card
// as if this spec had already triggered it. This was the mechanism behind
// #1901's zone-2 race. Reset unconditionally (outside e2e-k3d, see below)
// rather than auditing every spec that touches the shared baseline rows: it
// is a no-op for a pristine worker and cheap otherwise, and every spec
// depends on the `app` fixture below.
//
// Skipped entirely in e2e-k3d mode: that mode is workers: 1 with one shared
// backend and a real cluster for the whole run (fixtures/workerBackend.ts),
// so a mid-run reset could plausibly race a real, in-flight deploy's own
// activity/usage observation in ways the default inert mode's never-deployed
// baseline rows cannot. That mode already has its own determinism rules
// (playwright/AGENTS.md § "Opt-in k3d e2e mode"); this fix targets the
// default suite's shared seeded baseline specifically.
async function resetSharedBaselineObservations(
  baseURL: string,
  request: import('@playwright/test').APIRequestContext,
): Promise<void> {
  if (e2eK3dEnabled()) {
    return;
  }
  for (const method of [
    'ResetEnvironmentActivityObservations',
    'ResetEnvironmentUsageObservations',
  ]) {
    const res = await request.post(`${baseURL}/__erun_invoke`, {
      data: { method, args: [] },
    });
    const envelope = (await res.json()) as { error?: string };
    if (envelope.error) {
      throw new Error(`${method} failed: ${envelope.error}`);
    }
  }
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
  app: async ({ page, workerBaseURL, request }, use) => {
    await resetSharedBaselineObservations(workerBaseURL, request);
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

// captureHoverCard writes a hover card's own screenshot with a bounded wait.
//
// A hover card exists only while the pointer rests on the row that raised it,
// and locator.screenshot() carries no timeout of its own: it waits for the
// element to be visible and stable for as long as its caller allows. A card
// that closes or reflows mid-capture therefore does not fail the capture, it
// silently consumes the entire convergence budget its caller was relying on to
// re-drive the step -- so the step never gets a second attempt and the spec
// reports a timeout with every assertion before it having passed. Bounding the
// capture costs one attempt instead of the whole budget.
export async function captureHoverCard(card: Locator, filePath: string): Promise<void> {
  await card.screenshot({ path: filePath, timeout: 2_000 });
}

export { expect };

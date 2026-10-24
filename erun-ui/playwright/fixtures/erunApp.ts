import { expect, type Locator, type Page } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import { injectTestStylesheet } from '../pages/testStylesheet.js';
import { test as base } from './workerBackend.js';
import { reapStubProcesses } from './stubProcesses.js';
import {
  SEED_TENANT,
  e2eK3dEnabled,
  isolatedRoot,
  removeEnvironment,
  seedEnvironment,
  seedHostEnvironment,
  seedRuntimeEnvironment,
  uniqueEnvironmentName,
} from './seedRoot.js';

export interface SeededEnvironment {
  tenant: string;
  environment: string;
}

// Backend activity/usage observations live for the whole worker, so an earlier spec's
// readings would leak onto this spec's baseline rows. Skipped in e2e-k3d mode, where a reset
// could race a real in-flight deploy.
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

// Specs that mutate per-env state use `seededEnv` so the shared baseline rows stay quiet.
export const test = base.extend<{
  app: AppShell;
  seededEnv: SeededEnvironment;
  seededRuntimeEnv: SeededEnvironment;
  seededHostEnv: SeededEnvironment;
  stubReaper: void;
}>({
  // Sessions park stub processes for their lifetime; reap after every spec so leaks stay bounded.
  stubReaper: [
    async ({}, use) => {
      await use();
      reapStubProcesses(isolatedRoot());
    },
    { auto: true },
  ],
  // A fixture timeout, because boot cost belongs to the machine, not the spec's 30s budget.
  // 60s stays above AppShell.open's 40s settle bound.
  app: [
    async ({ page, workerBaseURL, request }, use) => {
      await resetSharedBaselineObservations(workerBaseURL, request);
      const app = new AppShell(page);
      await app.open();
      await use(app);
    },
    { timeout: 60_000 },
  ],
  seededEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, SEED_TENANT, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  // A runtime-type env, for the sourceless deploy path.
  seededRuntimeEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedRuntimeEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, SEED_TENANT, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
  seededHostEnv: async ({ app }, use, testInfo) => {
    const environment = uniqueEnvironmentName(testInfo.title);
    seedHostEnvironment(SEED_TENANT, environment);
    await waitForSeededRow(app, SEED_TENANT, environment);
    await use({ tenant: SEED_TENANT, environment });
    removeEnvironment(SEED_TENANT, environment);
  },
});

// waitForSeededRow re-drives a reload until the env row appears: the watcher can miss events
// and a deduplicated refetch can resolve against a pre-write snapshot.
// The default timeout is the calling test's own budget.
export async function waitForSeededRow(
  app: AppShell,
  tenant: string,
  environment: string,
  timeoutMs = withTestBudget().timeout,
): Promise<void> {
  await expect(async () => {
    await app.reloadEnvironments();
    await app.sidebar
      .envRowButton(tenant, environment)
      .waitFor({ state: 'visible', timeout: 2_000 });
  }).toPass({ timeout: timeoutMs });
}

// captureHoverCard takes one bounded attempt; callers retry by re-hovering, and a nested
// retry loop could exceed the test timeout.
export async function captureHoverCard(card: Locator, filePath: string): Promise<void> {
  await card.screenshot({ path: filePath, timeout: 8_000 });
}

// withTestBudget points an assertion at the test's declared timeout; expect() otherwise
// uses expect.timeout, independent of test.setTimeout.
export function withTestBudget(): { timeout: number } {
  return { timeout: test.info().timeout };
}

// disablePopoverEntranceAnimation freezes the popover's zoom-in so geometry reads see it at rest.
const POPOVER_ENTRANCE_ANIMATION_OFF = [
  '[role="dialog"][data-state] {',
  '  animation: none !important;',
  '  transform: none !important;',
  '}',
].join('\n');

const POPOVER_ANIMATION_OFF_ATTR = 'data-erun-test-popover-animation-off';

export async function disablePopoverEntranceAnimation(page: Page): Promise<void> {
  await injectTestStylesheet(page, POPOVER_ANIMATION_OFF_ATTR, POPOVER_ENTRANCE_ANIMATION_OFF);
}

// constrainPopoverWidth narrows the popover below w-90 so long values overflow.
export async function constrainPopoverWidth(page: Page, width: string): Promise<void> {
  await injectTestStylesheet(
    page,
    'data-erun-test-popover-width',
    `[role="dialog"] { width: ${width} !important; }`,
  );
}

export { expect };

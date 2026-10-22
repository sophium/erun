import { expect, type Locator, type Page } from '@playwright/test';
import { AppShell } from '../pages/index.js';
import { injectTestStylesheet } from '../pages/testStylesheet.js';
import { test as base } from './workerBackend.js';
import { reapStubProcesses } from './stubProcesses.js';
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
// the hover-card layout spec's zone-2 race. Reset unconditionally (outside e2e-k3d, see below)
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
  stubReaper: void;
}>({
  // stubReaper ends the stub processes a spec leaves behind. The desktop spawns
  // them (`erun open` for a tab session, `claude` for an orchestrator session)
  // and parks them for the life of the session, so a spec that opens one and
  // never closes it leaks a live process into the rest of the run — a full ALL
  // run reached 117, all competing with the suite for the gate's CPUs (#2512).
  // Reaping on every spec's teardown bounds the population to one spec's worth;
  // the worker teardown (fixtures/workerBackend.ts) sweeps whatever the last spec
  // left. Automatic rather than opt-in, so no spec has to remember it.
  stubReaper: [
    async ({}, use) => {
      await use();
      reapStubProcesses();
    },
    { auto: true },
  ],
  // The fixture's own timeout, not the test's: app.open() is a BOOT, and its
  // cost is the machine's, not the spec's. Every spec pays it in setup, so
  // charging it to the 30s test budget means a contended gate reports "Test
  // timeout ... while setting up app" against whichever spec happened to boot
  // on the busy worker -- which is how titlebar-whip-action.spec.ts:231 failed
  // two full-suite gates in a row while 554 other specs booted fine.
  //
  // playwright.config.ts already raises the global timeout to 90s on Windows
  // for the same reason, so a slower environment earning a larger boot budget
  // is this suite's established shape. 60s sits above AppShell.open's own 40s
  // settle bound so that bound stays reachable; the spec body keeps its 30s.
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
// timeoutMs defaults to the budget the CALLING TEST declared, not to a number
// this helper picked: `toPass({ timeout: N })` takes `min(test deadline,
// now + N)`, so a fixed default is a second, independent cap that a test
// which declared more would still fail at, with its own budget unspent. A
// spec that seeds an unusually large population before calling this (many
// environments and/or orchestrators in one go) may still pass a larger
// number, but the default no longer undercuts anyone.
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

// captureHoverCard writes a hover card's own screenshot with a single bounded
// attempt.
//
// A hover card exists only while the pointer rests on the row that raised it,
// and locator.screenshot() carries no timeout of its own: it waits for the
// element to be visible and stable for as long as its caller allows, so an
// unbounded call risks spending a whole convergence budget on one attempt.
// Under real contention the stability half of that wait -- two consecutive
// animation frames with an unchanged bounding box -- can take several seconds
// to observe even though the card is fine, so the bound here is generous
// (8s) rather than the tight budget a quiet machine would need. This used to
// retry itself, but every caller now reads the card's content (and takes this
// screenshot) from inside its own re-drivable hover+read block -- see
// sidebar-orchestrator-hover-card-activity.spec.ts's withOrchestratorCard and
// the live-update test's rehover() -- which already recovers a dropped card
// by re-hovering. Retrying here too would stack two convergence loops inside
// one test timeout and risk exceeding it before either one settles; one
// bounded attempt per outer retry is enough.
export async function captureHoverCard(card: Locator, filePath: string): Promise<void> {
  await card.screenshot({ path: filePath, timeout: 8_000 });
}

// withTestBudget is the timeout option a convergence step should carry when it
// is waiting for the app to reach a state: the budget the test itself declared.
//
// Playwright bounds the two idioms differently, and the difference is the whole
// of the contention class this exists to close. `waitFor({ state })` and a bare
// `toPass()` resolve to the enclosing test's own deadline, so a step inside a
// 60s test may take 60s. `expect(...)` and `expect.poll(...)` do not: with no
// explicit timeout they resolve to `expect.timeout` (playwright.config.ts sets
// 10s on POSIX), which is independent of `test.setTimeout(...)`. A step that is
// merely slow -- not wrong -- therefore reds a test that declared it could take
// 60s, at 10s, with 50s unused; and removing that explicit timeout changes
// nothing, because it only lands back on the same 10s default.
//
// So the fix is not to widen a number but to point the assertion at the budget
// the test already declared. A step that never converges still fails, now at
// the deadline the test chose rather than at a default it never chose.
export function withTestBudget(): { timeout: number } {
  return { timeout: test.info().timeout };
}

// disablePopoverEntranceAnimation freezes the Radix popover's entrance
// animation before any geometry read, so the card is measured at rest.
//
// PopoverContent (erun-kit/src/components/ui/popover.tsx) carries
// `data-[state=open]:animate-in ... zoom-in-95`, so every open runs a ~150ms
// transform. `toBeVisible()` resolves the instant the element is visible, not
// once that transform settles, so a bounding-box or colour read taken right
// after can land mid-transition and report a smaller-than-rest size --
// indistinguishable from a real difference between two cards. The animation
// has to be off before the first measurement, not waited out.
//
// Injected through the shared pages/testStylesheet.ts, whose own comment
// records why that is an evaluate rather than page.addStyleTag. Its contract
// -- the popover really is at rest afterwards -- is pinned by
// tests/areas/sidebar/sidebar-hovercard-animation-off.spec.ts.
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

// constrainPopoverWidth narrows the popover below its own fixed w-90, for the
// clipping cases that need a long value to overflow whatever the card is
// later sized to.
export async function constrainPopoverWidth(page: Page, width: string): Promise<void> {
  await injectTestStylesheet(
    page,
    'data-erun-test-popover-width',
    `[role="dialog"] { width: ${width} !important; }`,
  );
}

export { expect };

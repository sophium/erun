import type { Page } from '@playwright/test';

import { captureHoverCard, expect, test } from '../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../fixtures/seedRoot.js';

// The orchestrator hover card named its linked environments and said
// nothing about either one — `Environments: petios / rihards-review, erun /
// local-ideas`, two names, no state. Both missing signals (each environment's
// own activity, and the orchestrator's own pacing/nudge state) are already
// computed elsewhere in the backend (environment_activity.go's poller,
// orchestrator_pacing.go's session state); this spec locks in the join, not
// a new collection path — see orchestratorEnvironmentActivity.ts and
// orchestratorNudgeSummary.ts for the reduction under test.
//
// Against origin/main every assertion below that checks for more than the
// bare "tenant / environment" text fails, because orchestratorEnvInfo carried
// no activity field at all and the card rendered nothing else.

const RUNNING_SESSION_ID = 4242;

function snapshot(overrides: Record<string, unknown>) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    ...overrides,
  };
}

async function stubOrchestratorList(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [body] }),
      });
    }
    await route.continue();
  });
}

function card(page: Page) {
  return page.getByRole('dialog', { name: `${SEED_ORCHESTRATOR} details` });
}

// Radix's PopoverContent (erun-kit/components/ui/popover.tsx) runs a ~150ms
// zoom-in-95 + slide-in entrance transform on every open. `toBeVisible()`
// resolves the instant the element is visible, not once that transform
// settles, so a `boundingBox()` read taken right after can land mid-transition
// and report a smaller-than-rest size. Mirrors the same workaround in
// sidebar-hovercard-layout.spec.ts.
async function disablePopoverEntranceAnimation(page: Page): Promise<void> {
  await page.addStyleTag({
    content:
      '[role="dialog"][data-state] { animation: none !important; transform: none !important; }',
  });
}

test.describe('orchestrator hover card environment and pacing state', () => {
  test('a linked environment names what it is doing, not just its name (red-then-green)', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: 'build',
            directory: '/tmp/a',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy: true,
              detail: 'holding: gradle-build',
            },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('acme / build');
    // This is the part that fails on origin/main: the row there is just the
    // name, with no rendered activity at all.
    await expect(card(page)).toContainText('Busy — holding: gradle-build');

    await captureHoverCard(card(page), 'test-results/1383-visual/one-environment-busy.png');
  });

  test('two environments render distinct states side by side', async ({ app, page }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: 'build',
            directory: '/tmp/a',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy: true,
              detail: 'holding: gradle-build',
            },
          },
          {
            tenant: 'acme',
            environment: 'prod',
            directory: '/tmp/b',
            activity: { reachable: true, observed: true, outage: false, busy: false },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('Busy — holding: gradle-build');
    await expect(card(page)).toContainText('Idle');

    await captureHoverCard(card(page), 'test-results/1383-visual/two-environments.png');
  });

  test('three environments stay scannable and each state reads distinctly, including nudge state', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        // nudgeCount/nudgeCapped are the cap's own live budget (0/false here,
        // as if the session had already answered); autoNudgeCount is the
        // cumulative history the card reads for "Nudged Nx" -- see
        // orchestratorNudgeSummary.ts.
        nudgeCount: 0,
        nudgeCapped: false,
        autoNudgeCount: 3,
        lastAutoNudgeAtUnix: Math.floor(Date.now() / 1000) - 125,
        environments: [
          {
            tenant: 'acme',
            environment: 'build',
            directory: '/tmp/a',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy: true,
              detail: 'holding: gradle-build',
            },
          },
          {
            tenant: 'acme',
            environment: 'prod',
            directory: '/tmp/b',
            activity: { reachable: true, observed: true, outage: false, busy: false },
          },
          {
            tenant: 'acme',
            environment: 'staging',
            directory: '/tmp/c',
            activity: { reachable: true, observed: true, outage: true, busy: false },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Busy — holding: gradle-build');
    await expect(dialog).toContainText('Idle');
    await expect(dialog).toContainText('Lost connection');
    // Nudged more than once, and not capped, is its own distinguishable state.
    await expect(dialog).toContainText('Nudged 3x');

    await captureHoverCard(
      card(page),
      'test-results/1383-visual/three-environments-and-nudges.png',
    );
  });

  // This card is titled with the *orchestrator*, a host-side session with its
  // own MCP client, so a line here must never assert anything about the
  // orchestrator's own reach -- only what this desktop itself observed. An
  // environment with no activity reading at all (no local forward, and no
  // answer from the pod-exec fallback either) is genuinely unknown from the
  // orchestrator's point of view, not confirmed closed, so the line names the
  // desktop explicitly rather than asserting a bare "not open" that would
  // read as a claim about the orchestrator.
  test('an environment with no reading from this desktop names the desktop, not a bare "not open"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [{ tenant: 'acme', environment: 'never-opened', directory: '/tmp/a' }],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    // Scoped to the environment's own row: the card's unrelated "Doing" row
    // legitimately says "Idle, waiting for input" about the orchestrator's
    // own turn state, which must not be confused with this environment's
    // activity state.
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / never-opened' });
    await expect(environmentRow).toContainText('No forward from this desktop');
    await expect(environmentRow).not.toContainText('Not open here');
    await expect(environmentRow).not.toContainText('Idle');

    await captureHoverCard(dialog, 'test-results/1383-visual/no-forward-environment.png');
  });

  // The regression this locks in: an environment not open in this desktop —
  // driven instead by a CLI orchestrator or an agent over MCP from another
  // machine — used to read as "Not open here" even while genuinely busy,
  // because the desktop never asked it anything without a local forward. The
  // Go poller (environment_activity.go's observeEnvironmentActivityViaPod)
  // now asks such an environment directly over its own runtime pod, so the
  // activity this card renders can say "busy" for an environment this
  // desktop never opened. Against the un-fixed reduction this env would
  // carry no activity at all (undefined, the "never opened" shape above) and
  // render "Not open here" instead.
  test('an environment busy from elsewhere reads busy, not "Not open here"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: 'ux',
            directory: '/tmp/a',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy: true,
              detail: 'holding: full-test-suite',
            },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / ux' });
    await expect(environmentRow).toContainText('Busy — holding: full-test-suite');
    await expect(environmentRow).not.toContainText('Not open here');

    await captureHoverCard(dialog, 'test-results/1383-visual/busy-from-elsewhere.png');
  });

  // The other half of the fix: a real attempt to reach an unopened environment
  // that did not come back (a pod exec that errored, or the environment
  // genuinely not running) must read distinctly from "nobody has ever asked",
  // and must name the recovery action rather than leaving a dead end.
  test('a failed attempt to confirm an unopened environment names the recovery action', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: 'stale',
            directory: '/tmp/a',
            activity: {
              reachable: false,
              observed: false,
              outage: false,
              checkFailed: true,
              busy: false,
            },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / stale' });
    await expect(environmentRow).toContainText('open it to check directly');
    await expect(environmentRow).not.toContainText('Not open here');

    await captureHoverCard(dialog, 'test-results/1383-visual/check-failed-environment.png');
  });

  test('an environment in outage reads distinctly from idle and unreachable', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: 'build',
            directory: '/tmp/a',
            activity: { reachable: false, observed: false, outage: true, busy: false },
          },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / build' });
    await expect(environmentRow).toContainText('Lost connection');
    await expect(environmentRow).not.toContainText('Not open here');
    await expect(environmentRow).not.toContainText('Idle');

    await captureHoverCard(dialog, 'test-results/1383-visual/outage-environment.png');
  });

  test('a long environment name and a long busy detail elide instead of blowing out the card', async ({
    app,
    page,
  }) => {
    const longEnvironment = 'a-very-long-environment-name-that-keeps-going-and-going';
    const longDetail =
      'holding: ' +
      'gradle-build-with-an-unusually-long-task-name-attached-to-it '.repeat(3).trim();
    await stubOrchestratorList(
      page,
      snapshot({
        environments: [
          {
            tenant: 'acme',
            environment: longEnvironment,
            directory: '/tmp/a',
            activity: {
              reachable: true,
              observed: true,
              outage: false,
              busy: true,
              detail: longDetail,
            },
          },
        ],
      }),
    );
    await app.reboot();
    await disablePopoverEntranceAnimation(page);

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    // The full strings are in the DOM (rendered, not dropped) — truncation is
    // a CSS ellipsis, not a data loss — so the card's fixed width must not
    // grow past the popover's own w-72.
    await expect(dialog).toContainText(longEnvironment);
    const cardBox = await dialog.boundingBox();
    expect(cardBox?.width).toBeLessThan(320);

    await captureHoverCard(dialog, 'test-results/1383-visual/long-values.png');
  });

  test('a capped orchestrator names the recovery, distinct from a session that was never nudged', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        nudgeCount: 6,
        nudgeCapped: true,
        lastNudgeAtUnix: Math.floor(Date.now() / 1000) - 60,
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Stopped nudging after 6 attempts');
    await expect(dialog).toContainText('reply or restart');

    await captureHoverCard(dialog, 'test-results/1383-visual/capped-nudge.png');
  });

  // A restored persisted-history read is a Go-side concern (a real desktop
  // restart, or the on-disk file, is unreachable from this harness) --
  // covered by erun-ui/orchestrator_nudge_history_test.go. What IS reachable
  // here is the frontend's own rendering rule once the backend reports the
  // unreadable flag: nudgeHistoryUnreadable must read as a distinct,
  // actionable state, never silently collapse onto "Not nudged".
  test('an unreadable persisted history reads as unavailable, never as "Not nudged"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({ nudgeHistoryUnreadable: true }));
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Nudge history unavailable');
    await expect(dialog).not.toContainText('Not nudged');
  });

  test('a stopped orchestrator with no nudge history reports no nudge row at all', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({ status: 'stopped', sessionId: 0 }));
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).not.toContainText('Nudges');
  });

  // The persisted cumulative history survives a Stop (orchestrator_nudge_
  // history.go), so a stopped orchestrator that WAS nudged before it stopped
  // must still show its history -- the fix this locks in: before it, the row
  // was hidden unconditionally whenever status was not "running", which
  // would have thrown the backend's restored history away on the one screen
  // an operator checking "did the pacer actually run" looks at first.
  test('a stopped orchestrator with real nudge history still reports it', async ({ app, page }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        status: 'stopped',
        sessionId: 0,
        autoNudgeCount: 4,
        lastAutoNudgeAtUnix: Math.floor(Date.now() / 1000) - 300,
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Nudges');
    await expect(dialog).toContainText('Nudged 4x');
  });

  // Linked through a stubbed ListOrchestrators (like the tests above) rather
  // than the suite's static pw/alpha link: alpha is the desktop's own
  // auto-opened default environment, whose real idle/activity polling would
  // keep overwriting the injected event underneath this test. A seededEnv is
  // never opened, so nothing but the driven event touches its activity.
  test('a card holding an outage clears it once the same environment recovers, and the row agrees', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubOrchestratorList(
      page,
      snapshot({ environments: [{ tenant, environment, directory: '/tmp/a' }] }),
    );
    await app.reboot();

    const dot = app.sidebar.envOpenDot(tenant, environment);
    const dialog = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);

    // A retry that re-hovers a row the pointer never left is a no-op — the
    // browser only fires mouseenter on a genuine boundary crossing, so a
    // popover that closed for any other reason (e.g. the screenshot below
    // scrolling it out of view) would then never reopen. Moving off first
    // guarantees every retry re-triggers a real enter.
    async function rehover(): Promise<void> {
      await page.mouse.move(0, 0);
      await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    }

    // This is the transition the bug lost: a card that already rendered once
    // must pick up a later event, not just whatever the fetch it booted from
    // handed it.
    await driveEnvActivity(
      page,
      { tenant, environment, reachable: false, observed: false, outage: true, busy: false },
      async () => {
        await rehover();
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('Lost connection', { timeout: 1_000 });
        await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 1_000 });
        // Taken while still converged and hovered — a screenshot outside this
        // callback can race the popover's own close-on-mouse-leave timer.
        await captureHoverCard(dialog, 'test-results/orchestrator-card-live-state/card-outage.png');
      },
    );

    await driveEnvActivity(
      page,
      { tenant, environment, reachable: true, observed: true, outage: false, busy: false },
      async () => {
        await rehover();
        await expect(dialog).toContainText('Idle', { timeout: 1_000 });
        await expect(dialog).not.toContainText('Lost connection', { timeout: 1_000 });
        await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 1_000 });
        await captureHoverCard(
          dialog,
          'test-results/orchestrator-card-live-state/card-recovered.png',
        );
      },
    );
  });
});

interface EnvActivityEvent {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  outage?: boolean;
  busy: boolean;
  detail?: string;
}

// Mirrors erun-ui/environment_activity.go's env-activity event. The backend's
// own sweep also runs on a timer against this seeded (inert) env and can
// overwrite the injected value with its own "unreachable" observation, so
// every assertion driven by this helper is re-driven until it converges,
// bounded by a real timeout rather than a guessed delay.
async function driveEnvActivity(
  page: Page,
  event: EnvActivityEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvActivity(page, event);
    await assertions();
  }).toPass({ timeout: 20_000 });
}

async function emitEnvActivity(page: Page, payload: EnvActivityEvent): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', event);
  }, payload);
}

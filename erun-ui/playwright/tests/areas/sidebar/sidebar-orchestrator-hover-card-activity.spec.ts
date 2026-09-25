import type { Locator, Page } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import {
  captureHoverCard,
  constrainPopoverWidth,
  disablePopoverEntranceAnimation,
  expect,
  test,
} from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';

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

// Reads the open orchestrator hover card as one retryable unit instead of a
// bare hover followed by a sequence of independent assertions.
//
// The card's open state belongs to the hovered row's own React state, so any
// re-render can drop it while the pointer still rests there (see
// erun-ui/playwright/AGENTS.md's hover-card bullet) -- under contention this
// is not rare, and once dropped nothing reopens it because the pointer never
// left. A sequence of separate `expect(dialog)...` calls after a single hover
// has no way back from a mid-sequence drop: whichever assertion runs after
// the drop fails against a dialog that no longer exists. Retrying the whole
// hover-then-read as one unit recovers by re-hovering, the same way the
// live-update test below's rehover() does by hand.
async function withOrchestratorCard(
  page: Page,
  app: AppShell,
  read: (dialog: Locator) => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await read(card(page));
  }).toPass({ timeout: 25_000 });
}

// CHECK_FAILED_LINE is the prose the check-failed row renders: a status clause
// the operator cannot act on, then the remedy they can.
const CHECK_FAILED_LINE = "Can't confirm from here — open it to check directly";

// clippingReport answers the question a text query cannot: is the element's
// rendered text wider than its own visible box (the CSS-ellipsis case), and
// does the END of the string -- the remedy -- land inside that box. A
// `toContainText` assertion passes on either answer, because a clipped string
// is still in the DOM; that is why this defect hid behind one.
async function clippingReport(
  locator: Locator,
  tail: string,
): Promise<{ text: string; overflowBy: number; tailInsideBox: boolean }> {
  return locator.evaluate((el, tailText) => {
    const node = el.firstChild;
    let tailInsideBox = false;
    if (node && node.nodeType === Node.TEXT_NODE) {
      const content = node.textContent ?? '';
      const start = content.lastIndexOf(tailText);
      if (start >= 0) {
        const range = document.createRange();
        range.setStart(node, start);
        range.setEnd(node, start + tailText.length);
        const tailRect = range.getBoundingClientRect();
        const box = el.getBoundingClientRect();
        tailInsideBox =
          tailRect.width > 0 &&
          tailRect.right <= box.right + 1 &&
          tailRect.bottom <= box.bottom + 1;
      }
    }
    return {
      text: (el.textContent ?? '').trim(),
      overflowBy: el.scrollWidth - el.clientWidth,
      tailInsideBox,
    };
  }, tail);
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('acme / build');
      // This is the part that fails on origin/main: the row there is just the
      // name, with no rendered activity at all.
      await expect(dialog).toContainText('Busy — holding: gradle-build');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/one-environment-busy.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Busy — holding: gradle-build');
      await expect(dialog).toContainText('Idle');

      await captureHoverCard(dialog, artifactPath('test-results/1383-visual/two-environments.png'));
    });
  });

  test('three environments stay scannable and each state reads distinctly, including nudge state', async ({
    app,
    page,
  }) => {
    // This test's read is the heaviest in the block -- four toContainText
    // checks plus a capture per withOrchestratorCard attempt, versus one or
    // two for its siblings -- so its legitimate per-attempt cost under
    // contention can consume the suite's global 30s per-test timeout before
    // withOrchestratorCard's own 25s retry budget converges (root AGENTS.md's
    // "no flaky tests" gate needs this to be a real budget increase, not a
    // race against the whole-test clock the retry below would still lose).
    test.setTimeout(60_000);
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Busy — holding: gradle-build');
      await expect(dialog).toContainText('Idle');
      await expect(dialog).toContainText('Lost connection');
      // Nudged more than once, and not capped, is its own distinguishable state.
      await expect(dialog).toContainText('Nudged 3x');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/three-environments-and-nudges.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      // Scoped to the environment's own row: the card's unrelated "Doing" row
      // legitimately says "Idle, waiting for input" about the orchestrator's
      // own turn state, which must not be confused with this environment's
      // activity state.
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / never-opened' });
      await expect(environmentRow).toContainText('No forward from this desktop');
      await expect(environmentRow).not.toContainText('Not open here');
      await expect(environmentRow).not.toContainText('Idle');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/no-forward-environment.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / ux' });
      await expect(environmentRow).toContainText('Busy — holding: full-test-suite');
      await expect(environmentRow).not.toContainText('Not open here');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/busy-from-elsewhere.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / stale' });
      await expect(environmentRow).toContainText('open it to check directly');
      await expect(environmentRow).not.toContainText('Not open here');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/check-failed-environment.png'),
      );
    });
  });

  // The test above proves the remedy is *rendered*; this one proves it is
  // *visible*. The card used to render that line through a bare `truncate`, so
  // at a constrained width the clip kept "Can't confirm from here —" and
  // dropped "open it to check directly" -- the only thing the row exists to
  // deliver in this state. A text assertion cannot see that (the string is in
  // the DOM either way), so this measures geometry instead, at a width narrow
  // enough that no future card sizing can let the string fit by accident.
  // Wrapping is the app's rule for explanatory prose: InlineAlert's
  // `[overflow-wrap:anywhere]`, the deploy overlay, the Jobs tab's command and
  // output. See erun-ui/frontend/src/components/app/Sidebar.HoverCardRow.tsx
  // for the identifier-clips / prose-wraps split this row had diverged from.
  test('the check-failed remedy wraps at a constrained width instead of being clipped', async ({
    app,
    page,
  }) => {
    // Same budget and same reason as the three-environment test above:
    // withOrchestratorCard's own 25s retry must not race the whole-test clock.
    test.setTimeout(60_000);
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
    await disablePopoverEntranceAnimation(page);
    // Deliberately width-constrained, the way the deploy overlay's own
    // narrow-width capture is: 11rem is well below the card's fixed w-90, so
    // the string cannot fit on one line however the card is later sized.
    await constrainPopoverWidth(page, '11rem');

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / stale' });
      // `.last()` is the innermost match: the row and its value column contain
      // this text too, and neither of those ever clips (no overflow of their
      // own), which is exactly the false green this assertion must not take.
      const status = environmentRow.getByText(CHECK_FAILED_LINE).last();
      await expect(status).toBeVisible();

      const report = await clippingReport(status, 'directly');
      // Not dropped...
      expect(report.text).toBe(CHECK_FAILED_LINE);
      // ...and not hidden behind an ellipsis: nothing overflows, so the remedy
      // half renders on the row instead of being the part that is cut.
      expect(report.overflowBy).toBeLessThanOrEqual(1);
      expect(report.tailInsideBox).toBe(true);

      await captureHoverCard(
        dialog,
        artifactPath('test-results/2352-remedy/check-failed-remedy-wraps.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / build' });
      await expect(environmentRow).toContainText('Lost connection');
      await expect(environmentRow).not.toContainText('Not open here');
      await expect(environmentRow).not.toContainText('Idle');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/outage-environment.png'),
      );
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      // The full strings are in the DOM (rendered, not dropped) — truncation
      // is a CSS ellipsis, not a data loss — so the card's fixed width must
      // not grow past the popover's own w-90 (360px).
      await expect(dialog).toContainText(longEnvironment);
      const cardBox = await dialog.boundingBox();
      expect(cardBox?.width).toBeLessThanOrEqual(360);

      await captureHoverCard(dialog, artifactPath('test-results/1383-visual/long-values.png'));
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Stopped nudging after 6 attempts');
      await expect(dialog).toContainText('reply or restart');

      await captureHoverCard(dialog, artifactPath('test-results/1383-visual/capped-nudge.png'));
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Nudge history unavailable');
      await expect(dialog).not.toContainText('Not nudged');
    });
  });

  test('a stopped orchestrator with no nudge history reports no nudge row at all', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({ status: 'stopped', sessionId: 0 }));
    await app.reboot();

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).not.toContainText('Nudges');
    });
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

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Nudges');
      await expect(dialog).toContainText('Nudged 4x');
    });
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
    // Two sequential driveEnvActivity phases below each retry for up to 20s
    // of their own, so their legitimate combined cost under contention can
    // approach the suite's global 30s per-test timeout before either
    // converges (root AGENTS.md's "no flaky tests" gate needs this to be a
    // real budget increase, not a race against the whole-test clock the
    // bounded retries below would still lose).
    test.setTimeout(90_000);
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
        await captureHoverCard(
          dialog,
          artifactPath('test-results/orchestrator-card-live-state/card-outage.png'),
        );
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
          artifactPath('test-results/orchestrator-card-live-state/card-recovered.png'),
        );
      },
    );
  });

  // A session this desktop did not launch -- started in a terminal, or left by
  // a previous desktop instance -- is read and displayed like any other, but
  // the pacer decides only for sessions the desktop holds, so its nudge count
  // simply never moves. Nothing in that number separates it from an
  // orchestrator erun has just checked and found nothing to do about, which is
  // the whole of the defect.
  //
  // The read model behind this flag is covered by
  // TestListOrchestratorsMarksAConfiguredOrchestratorItCannotPace in
  // erun-ui/orchestrator_pacing_test.go (it needs a real report file and a real
  // config, which the headless harness deliberately does not stage for this
  // card); this spec locks the rendered surface down to the same JSON contract.
  test('an orchestrator this desktop cannot pace says so, instead of reading as freshly checked', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        // Stopped here while its own hooks keep reporting from wherever it is
        // really running: the state that used to be indistinguishable from a
        // session that needed nothing.
        status: 'stopped',
        sessionId: 0,
        pacingUnreachable: true,
      }),
    );
    await app.reboot();

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const nudgeRow = dialog.locator('dd').filter({ hasText: 'Not paced from this desktop' });
      // Both halves of the distinction, on one row: this desktop has not nudged
      // it, and cannot.
      await expect(nudgeRow).toContainText('Not nudged');
      await expect(nudgeRow).toContainText('erun has no session for it here');
      // Scoped to this desktop on purpose -- the session may be healthy and
      // paced by something else, so the line must not read as a claim about
      // the session itself.
      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/unpaced-from-this-desktop.png'),
      );
    });
  });

  // The inverse, which matters just as much: an orchestrator this desktop does
  // hold a session for is paced here, and must never be told otherwise.
  test('an orchestrator this desktop owns is never told it is not paced here', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({ pacingUnreachable: false }));
    await app.reboot();

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      await expect(dialog).not.toContainText('Not paced from this desktop');
    });
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

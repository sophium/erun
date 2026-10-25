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

// The orchestrator card joins each linked env's activity and the orchestrator's nudge state
// (orchestratorEnvironmentActivity.ts, orchestratorNudgeSummary.ts).

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

// A re-render can drop the card while the pointer rests on the row, so hover and read retry as one
// unit.
async function withOrchestratorCard(
  page: Page,
  app: AppShell,
  read: (dialog: Locator) => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await read(card(page));
  }).toPass();
}

const CHECK_FAILED_LINE = "Can't confirm from here — open it to check directly";

// Reports whether the text overflows its box and whether its end is visible; toContainText can't
// tell.
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
    // The heaviest read in the block; the helper's retry is uncapped so this budget bounds it.
    test.setTimeout(60_000);
    await stubOrchestratorList(
      page,
      snapshot({
        // nudgeCount/nudgeCapped are the live cap budget; autoNudgeCount is the cumulative history
        // for "Nudged Nx".
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
      await expect(dialog).toContainText('Nudged 3x');

      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/three-environments-and-nudges.png'),
      );
    });
  });

  // With no reading from this desktop the line names the desktop, not a bare "not open" about the
  // orchestrator.
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
      // The Doing row's "Idle" is the orchestrator's state, not this env's.
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

  // An env not open here is probed over its runtime pod, so it can read busy.
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

  // A failed probe must read distinctly from "never asked" and name the recovery.
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

  // The remedy must be visible, not clipped; prose wraps while identifiers truncate.
  test('the check-failed remedy wraps at a constrained width instead of being clipped', async ({
    app,
    page,
  }) => {
    // See the three-environment test above.
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
    // 11rem is well below w-90, so the string can't fit by accident.
    await constrainPopoverWidth(page, '11rem');

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / stale' });
      // `.last()` is the innermost match; outer elements never clip.
      const status = environmentRow.getByText(CHECK_FAILED_LINE).last();
      await expect(status).toBeVisible();

      const report = await clippingReport(status, 'directly');
      expect(report.text).toBe(CHECK_FAILED_LINE);
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
      // Truncation is CSS ellipsis, so the card must stay within w-90 (360px).
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

  // Persisted-history reads are covered by internal/orchestrator/nudge_history_test.go; here the unreadable
  // flag must not collapse to "Not nudged".
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

  // Nudge history survives a Stop, so a stopped orchestrator still shows it.
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

  // A seeded env instead of alpha, whose real polling would overwrite the injected event.
  test('a card holding an outage clears it once the same environment recovers, and the row agrees', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Two uncapped driveEnvActivity phases share this budget.
    test.setTimeout(90_000);
    const { tenant, environment } = seededEnv;
    await stubOrchestratorList(
      page,
      snapshot({ environments: [{ tenant, environment, directory: '/tmp/a' }] }),
    );
    await app.reboot();

    const dot = app.sidebar.envOpenDot(tenant, environment);
    const dialog = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);

    // Move off first: re-hovering a row the pointer never left fires no mouseenter.
    async function rehover(): Promise<void> {
      await page.mouse.move(0, 0);
      await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    }

    // A rendered card must pick up a later event.
    await driveEnvActivity(
      page,
      { tenant, environment, reachable: false, observed: false, outage: true, busy: false },
      async () => {
        await rehover();
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('Lost connection', { timeout: 1_000 });
        await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 1_000 });
        // Capture while hovered; outside the callback the popover's close timer can race it.
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

  // The pacer only nudges sessions this desktop holds, so others must say they aren't paced here.
  // Read model covered by TestListOrchestratorsMarksAConfiguredOrchestratorItCannotPace.
  test('an orchestrator this desktop cannot pace says so, instead of reading as freshly checked', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        status: 'stopped',
        sessionId: 0,
        pacingUnreachable: true,
      }),
    );
    await app.reboot();

    await withOrchestratorCard(page, app, async (dialog) => {
      await expect(dialog).toBeVisible();
      const nudgeRow = dialog.locator('dd').filter({ hasText: 'Not paced from this desktop' });
      await expect(nudgeRow).toContainText('Not nudged');
      await expect(nudgeRow).toContainText('erun has no session for it here');
      // Scoped to this desktop: the session may be paced elsewhere.
      await captureHoverCard(
        dialog,
        artifactPath('test-results/1383-visual/unpaced-from-this-desktop.png'),
      );
    });
  });

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

// Mirrors erun-ui/internal/runtimeview/env_activity.go's env-activity event; re-driven until it converges since
// the backend sweep can overwrite it.
async function driveEnvActivity(
  page: Page,
  event: EnvActivityEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvActivity(page, event);
    await assertions();
  }).toPass();
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

import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('acme / build');
    // This is the part that fails on origin/main: the row there is just the
    // name, with no rendered activity at all.
    await expect(card(page)).toContainText('Busy — holding: gradle-build');

    await card(page).screenshot({
      path: '/home/erun/.erun/outputs/1383-visual/one-environment-busy.png',
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('Busy — holding: gradle-build');
    await expect(card(page)).toContainText('Idle');

    await card(page).screenshot({
      path: '/home/erun/.erun/outputs/1383-visual/two-environments.png',
    });
  });

  test('three environments stay scannable and each state reads distinctly, including nudge state', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(
      page,
      snapshot({
        nudgeCount: 3,
        nudgeCapped: false,
        lastNudgeAtUnix: Math.floor(Date.now() / 1000) - 125,
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Busy — holding: gradle-build');
    await expect(dialog).toContainText('Idle');
    await expect(dialog).toContainText('Lost connection');
    // Nudged more than once, and not capped, is its own distinguishable state.
    await expect(dialog).toContainText('Nudged 3x');

    await card(page).screenshot({
      path: '/home/erun/.erun/outputs/1383-visual/three-environments-and-nudges.png',
    });
  });

  test('an environment never opened from this desktop reads unreachable, not idle', async ({
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    // Scoped to the environment's own row: the card's unrelated "Doing" row
    // legitimately says "Idle, waiting for input" about the orchestrator's
    // own turn state, which must not be confused with this environment's
    // activity state.
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / never-opened' });
    await expect(environmentRow).toContainText('Not open here');
    await expect(environmentRow).not.toContainText('Idle');

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1383-visual/unreachable-environment.png',
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    const environmentRow = dialog.locator('dd').filter({ hasText: 'acme / build' });
    await expect(environmentRow).toContainText('Lost connection');
    await expect(environmentRow).not.toContainText('Not open here');
    await expect(environmentRow).not.toContainText('Idle');

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1383-visual/outage-environment.png',
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    // The full strings are in the DOM (rendered, not dropped) — truncation is
    // a CSS ellipsis, not a data loss — so the card's fixed width must not
    // grow past the popover's own w-72.
    await expect(dialog).toContainText(longEnvironment);
    const cardBox = await dialog.boundingBox();
    expect(cardBox?.width).toBeLessThan(320);

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1383-visual/long-values.png' });
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Stopped nudging after 6 attempts');
    await expect(dialog).toContainText('reply or restart');

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1383-visual/capped-nudge.png' });
  });

  test('a stopped orchestrator reports no nudge row at all', async ({ app, page }) => {
    await stubOrchestratorList(page, snapshot({ status: 'stopped', sessionId: 0 }));
    await app.reboot();

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();

    const dialog = card(page);
    await expect(dialog).toBeVisible();
    await expect(dialog).not.toContainText('Nudges');
  });
});

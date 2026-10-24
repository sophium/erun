import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// Hovering an orchestrator row raises a hover card, like the environment row's
// EnvHoverCard. The busy spinner's IconTooltip exists only WHILE the
// orchestrator is spinning, so it cannot serve an idle orchestrator, the
// common case.
//
// The card says what the orchestrator is working on and for how long, from
// orchestratorInfo's `busyAtUnix`, not just `${name} is working` — the fact
// the spinner already conveys.

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

test.describe('orchestrator row hover card (#1343)', () => {
  test('a working orchestrator names what it is doing and for how long', async ({ app, page }) => {
    // 4m12s before now, so the label must render a real duration rather than
    // a bare "is working".
    const busyAtUnix = Math.floor(Date.now() / 1000) - 252;
    await stubOrchestratorList(
      page,
      snapshot({
        busy: true,
        busyAtUnix,
        environments: [
          { tenant: 'acme', environment: 'build', directory: '/tmp/a' },
          { tenant: 'acme', environment: 'prod', directory: '/tmp/b' },
        ],
      }),
    );
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('Running');
    // How long it has been working.
    await expect(card(page)).toContainText('Working, for 4m');
    // Linked scope, which the row itself has never shown.
    await expect(card(page)).toContainText('acme / build');
    await expect(card(page)).toContainText('acme / prod');
  });

  test('a running but idle orchestrator says so, distinctly from not started', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({ busy: false }));
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('Running');
    await expect(card(page)).toContainText('Idle, waiting for input');
    await expect(card(page)).toContainText('None linked');
  });

  test('a stopped orchestrator is hoverable too — the case with no spinner at all', async ({
    app,
    page,
  }) => {
    // An orchestrator that is not spinning still needs a hover surface; the
    // spinner's own tooltip cannot provide one.
    await stubOrchestratorList(page, snapshot({ status: 'stopped', sessionId: 0 }));
    await app.reboot();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);

    await expect(card(page)).toBeVisible();
    await expect(card(page)).toContainText('Stopped');
    await expect(card(page)).toContainText('Not started');
  });

  test('the card does not swallow the row click that opens the orchestrator', async ({
    app,
    page,
  }) => {
    // EnvHoverCard's own constraint, inherited deliberately: the hover surface
    // wraps the row, so it must not become a focus trap or eat the click.
    await stubOrchestratorList(page, snapshot({ busy: false }));
    await app.reboot();

    const row = app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR);
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await expect(card(page)).toBeVisible();
    await expect(row).toBeEnabled();
    await row.click();
    // The row stays operable; the card is decoration over it, not a modal.
    await expect(row).toBeVisible();
  });
});

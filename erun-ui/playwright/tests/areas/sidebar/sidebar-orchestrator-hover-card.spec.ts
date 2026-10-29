import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The busy spinner's tooltip exists only while spinning, so an idle
// orchestrator needs the row hover card; it reports work and duration.

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
    // 4m12s ago, so the label must render a real duration.
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
    await expect(card(page)).toContainText('Working, for 4m');
    // Linked scope, which the row itself never shows.
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
    // The hover surface wraps the row, so it must not trap focus or eat the click.
    await stubOrchestratorList(page, snapshot({ busy: false }));
    await app.reboot();

    const row = app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR);
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await expect(card(page)).toBeVisible();
    await expect(row).toBeEnabled();
    await row.click();
    await expect(row).toBeVisible();
  });
});

import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The picker lists every env it considered, disabling ineligible ones with a
// reason, rather than silently dropping them.

// Stubbed because the shared seeded baseline always carries eligible envs.
async function stubEnvCandidates(
  page: Page,
  candidates: {
    tenant: string;
    environment: string;
    eligible: boolean;
    defaultDirectory: string;
    mirrored: boolean;
    ineligibleReason: string;
  }[],
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestratorEnvCandidates') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: candidates }),
      });
      return;
    }
    await route.continue();
  });
}

test.describe('orchestrator Environments picker empty states', () => {
  test('no candidates at all says so and names the remedy', async ({ app, page }) => {
    await stubEnvCandidates(page, []);

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.environmentsEmptyMessage()).toBeVisible();
    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toHaveCount(0);

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });

  test('every candidate ineligible reads differently from no candidates at all', async ({
    app,
    page,
  }) => {
    await stubEnvCandidates(page, [
      {
        tenant: SEED_TENANT,
        environment: 'prod',
        eligible: false,
        defaultDirectory: '',
        mirrored: false,
        ineligibleReason:
          "This environment's type isn't recognized, so it can't be linked to an orchestrator.",
      },
    ]);

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toBeVisible();
    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toContainText('1');
    await expect(app.orchestratorDialog.environmentsEmptyMessage()).toHaveCount(0);

    const checkbox = app.orchestratorDialog.ineligibleEnvCheckbox(SEED_TENANT, 'prod');
    await expect(checkbox).toBeVisible();
    await expect(checkbox).toBeDisabled();
    await expect(app.orchestratorDialog.envIneligibleReason(SEED_TENANT, 'prod')).toContainText(
      "isn't recognized",
    );

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});

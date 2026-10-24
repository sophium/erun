import { expect, type Page } from '@playwright/test';

import {
  boundingBoxOf,
  expectDialogContentStaysWithinCard,
} from '../../../fixtures/boundingBox.js';
import { test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// DialogContent's implicit grid column sizes to its widest descendant, so an unbreakable string
// must not push in-flow content past the card.

async function stubOrchestratorEnvCandidate(
  page: Page,
  candidate: { tenant: string; environment: string; defaultDirectory: string; mirrored: boolean },
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestratorEnvCandidates') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [candidate] }),
      });
      return;
    }
    await route.continue();
  });
}

test.describe('dialog content stays within its own card', () => {
  test('Edit orchestrator dialog: name, environments, guidance and footer all stay within the card', async ({
    app,
  }) => {
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');
    // The seeded orchestrator's long guidance path is the unbreakable value.
    await expectDialogContentStaysWithinCard(
      app.orchestratorDialog.locator('Edit orchestrator'),
      'Edit orchestrator dialog',
    );
    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });

  test('Edit orchestrator dialog: a long Windows-style path stays within the card', async ({
    app,
    page,
  }) => {
    const longWindowsPath =
      'C:\\Users\\operator\\workspaces\\' + 'project-segment-'.repeat(8) + 'repo';
    await stubOrchestratorEnvCandidate(page, {
      tenant: SEED_TENANT,
      environment: SEED_ENV_ALPHA,
      defaultDirectory: longWindowsPath,
      mirrored: false,
    });
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');
    await expectDialogContentStaysWithinCard(
      app.orchestratorDialog.locator('Edit orchestrator'),
      'Edit orchestrator dialog (long Windows path)',
    );
    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });

  test('ERun settings dialog: a long cloud-provider value stays within the card', async ({
    app,
  }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    const provider = app.globalConfigDialog.cloudContextProviderTrigger();
    await provider.waitFor({ state: 'visible' });
    await provider.evaluate((btn) => {
      const span = btn.querySelector('[data-slot="select-value"]');
      if (!(span instanceof HTMLElement)) {
        throw new Error('select-value span not found on Cloud provider trigger');
      }
      span.textContent = `long.user.name+0123456789@aws-${'x'.repeat(80)}`;
    });

    await expectDialogContentStaysWithinCard(
      app.globalConfigDialog.locator(),
      'ERun settings dialog',
    );

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('Manage environment dialog: the General tab stays within the card', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expectDialogContentStaysWithinCard(
      app.manageDialog.locator(),
      'Manage environment dialog',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

test.describe('dialog content stays within its own card — deviceScaleFactor 1.5', () => {
  // Guards a scale-dependent regression explicitly.
  test.use({ deviceScaleFactor: 1.5 });

  test('Edit orchestrator dialog still holds at a non-1 device scale factor', async ({ app }) => {
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');
    await expectDialogContentStaysWithinCard(
      app.orchestratorDialog.locator('Edit orchestrator'),
      'Edit orchestrator dialog at deviceScaleFactor 1.5',
    );
    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });
});

// At a short window the panel must stay height-bounded with a scrolling field region, keeping Save
// on screen.
test.describe('dialog footer stays reachable at a short window height', () => {
  test('the Edit orchestrator Save button is fully within the viewport', async ({ app, page }) => {
    const viewportHeight = 520;
    await page.setViewportSize({ width: 1280, height: viewportHeight });
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    const save = app.orchestratorDialog.saveButton();
    await expect(save).toBeVisible();
    const box = await boundingBoxOf(save, 'the Edit orchestrator Save button');
    expect(box.y + box.height).toBeLessThanOrEqual(viewportHeight);

    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });
});

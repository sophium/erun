import type { Locator } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

test.describe('ERUN sidebar section', () => {
  test('renders above ENVIRONMENTS with orchestrators and header actions', async ({ app }) => {
    const section = app.sidebar.erunSection();
    await expect(section).toBeVisible();

    await expectRenderedAbove(section, app.sidebar.environmentsHeading());

    await expect(app.sidebar.erunSettingsButton()).toBeVisible();
    await expect(app.sidebar.erunDoctorButton()).toBeVisible();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect(app.sidebar.newOrchestratorButton()).toBeVisible();
  });

  test('Settings in the ERUN header opens the global config dialog', async ({ app }) => {
    await app.sidebar.erunSettingsButton().click();
    await expect(app.globalConfigDialog.locator()).toBeVisible();
  });

  test('orchestrator row shows a status dot and manages via a confirming "…" dialog', async ({
    app,
  }) => {
    // Status is shape + label, never colour alone.
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();

    await expect(app.sidebar.orchestratorDetailsButton(SEED_ORCHESTRATOR)).toBeVisible();
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    const editDialog = app.page.getByRole('dialog', { name: 'Edit orchestrator' });
    await expect(editDialog).toBeVisible();

    await editDialog.getByRole('button', { name: 'Delete', exact: true }).click();
    const confirmDialog = app.page.getByRole('dialog', { name: 'Delete orchestrator' });
    await expect(confirmDialog).toBeVisible();
    await expect(confirmDialog.getByRole('button', { name: 'Delete orchestrator' })).toBeVisible();

    // The seed is shared, so back out without deleting.
    await confirmDialog.getByRole('button', { name: 'Cancel' }).click();
    await app.page.keyboard.press('Escape');
    await expect(app.sidebar.orchestratorDetailsButton(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // Both guidance layers render with resolved paths; the IDE launch itself is covered by
  // erun-ui/orchestrator_guidance_test.go.
  test('manage dialog reveals both guidance layers with their resolved paths', async ({ app }) => {
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    await expect(app.orchestratorDialog.guidanceLabel('role')).toBeVisible();
    await expect(app.orchestratorDialog.guidanceLabel('shared')).toBeVisible();
    await expect(app.orchestratorDialog.guidanceRolePath(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.orchestratorDialog.guidanceSharedPath()).toBeVisible();

    for (const layer of ['role', 'shared'] as const) {
      for (const ide of ['VS Code', 'IntelliJ IDEA'] as const) {
        await expect(app.orchestratorDialog.guidanceOpenButton(layer, ide)).toBeEnabled();
      }
    }

    await app.page.keyboard.press('Escape');
  });
});

async function expectRenderedAbove(above: Locator, below: Locator): Promise<void> {
  const [aboveBox, belowBox] = await Promise.all([above.boundingBox(), below.boundingBox()]);
  expect(aboveBox, 'ERUN section is laid out').not.toBeNull();
  expect(belowBox, 'ENVIRONMENTS heading is laid out').not.toBeNull();
  if (aboveBox && belowBox) {
    expect(aboveBox.y).toBeLessThan(belowBox.y);
  }
}

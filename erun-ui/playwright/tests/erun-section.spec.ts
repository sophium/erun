import type { Locator } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../fixtures/seedRoot.js';

// The ERUN section is the host-side control plane (#795): a top-level block
// above ENVIRONMENTS holding the cross-env AI Orchestrators, with the erun-global
// actions (Settings, Doctor) carried in its header.
test.describe('ERUN sidebar section', () => {
  test('renders above ENVIRONMENTS with orchestrators and header actions', async ({ app }) => {
    const section = app.sidebar.erunSection();
    await expect(section).toBeVisible();

    await expectRenderedAbove(section, app.sidebar.environmentsHeading());

    // The erun-global actions that moved off the ENVIRONMENTS header.
    await expect(app.sidebar.erunSettingsButton()).toBeVisible();
    await expect(app.sidebar.erunDoctorButton()).toBeVisible();

    // AI Orchestrators: the seeded stopped orchestrator renders as a row (so the
    // empty state is gone), alongside the affordance to spawn another.
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect(app.sidebar.newOrchestratorButton()).toBeVisible();
  });

  test('Settings in the ERUN header opens the global config dialog', async ({ app }) => {
    await app.sidebar.erunSettingsButton().click();
    await expect(app.globalConfigDialog.locator()).toBeVisible();
  });

  // Full env-parity for the orchestrator row (#795): a shape-encoded status dot
  // and a single "…" that opens the management dialog, where delete is gated
  // behind an explicit confirmation — no inline stop/start/restart/delete.
  test('orchestrator row shows a status dot and manages via a confirming "…" dialog', async ({
    app,
  }) => {
    // Status is shape + label, never colour alone (shared StatusDotGlyph).
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();

    // The row's only action is "…", mirroring the environment row's edit button.
    await expect(app.sidebar.orchestratorDetailsButton(SEED_ORCHESTRATOR)).toBeVisible();
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    const editDialog = app.page.getByRole('dialog', { name: 'Edit orchestrator' });
    await expect(editDialog).toBeVisible();

    // Delete asks first: the initial Delete only swaps to a confirm view with a
    // distinct destructive action — nothing is removed yet.
    await editDialog.getByRole('button', { name: 'Delete', exact: true }).click();
    const confirmDialog = app.page.getByRole('dialog', { name: 'Delete orchestrator' });
    await expect(confirmDialog).toBeVisible();
    await expect(confirmDialog.getByRole('button', { name: 'Delete orchestrator' })).toBeVisible();

    // Back out without deleting (the seed is shared), close the dialog, and
    // confirm the row survived the confirmation gate.
    await confirmDialog.getByRole('button', { name: 'Cancel' }).click();
    await app.page.keyboard.press('Escape');
    await expect(app.sidebar.orchestratorDetailsButton(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // #1231: the manage dialog reveals both guidance layers rather than making
  // the operator guess the CLAUDE.<id>.md filename convention. This covers the
  // observable invariant the headless harness can reach — both entries render,
  // labelled for what they are, each with its resolved host path — without
  // clicking through to a real IDE launch: the harness stubs kubectl/helm/
  // docker/aws but not vscode/idea, and actually spawning (or failing to spawn)
  // a GUI editor from CI is exactly the live-infrastructure case AGENTS.md says
  // to name rather than reach for. The open-in-IDE branch itself (path
  // resolution, role-file seeding, unknown id/layer rejection) is covered by
  // the Go unit tests in erun-ui/orchestrator_guidance_test.go.
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

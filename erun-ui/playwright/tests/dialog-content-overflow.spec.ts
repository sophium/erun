import type { Page } from '@playwright/test';

import { expectDialogContentStaysWithinCard } from '../fixtures/boundingBox.js';
import { test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// Regression coverage for a shared-primitive defect in erun-kit's DialogContent
// (dialog.tsx): with no explicit grid-template-columns, the browser sizes the
// implicit single grid column to the max-content width of its widest
// descendant. An unbroken string with no min-w-0 anywhere above it (a long
// guidance-file path, in the Edit orchestrator dialog) forced that column —
// and every sibling grid item — wider than the card's own box, spilling
// in-flow content past the card while its background/border still painted at
// the correct, clamped width. The absolutely-positioned close button was
// unaffected, since absolute positioning is removed from grid flow and
// resolves against the card's own padding box, not the blown-out column.
//
// This reproduces in plain headless Chromium at deviceScaleFactor 1 with no
// platform involved, which rules out a Windows/high-DPI compositing mismatch:
// it is a box-model defect in the shared primitive, not a paint/raster one.

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
    // The seeded orchestrator already renders a real, long CLAUDE.<id>.md
    // guidance path and a real conversation UUID — the exact "long unbreakable
    // value" shape that reproduced the defect, with no stubbing required.
    await expectDialogContentStaysWithinCard(
      app.orchestratorDialog.locator('Edit orchestrator'),
      'Edit orchestrator dialog',
    );
    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });

  test('Edit orchestrator dialog: a long Windows-style path and a UUID both stay within the card', async ({
    app,
    page,
  }) => {
    // Direct coverage for the issue's own validation case: an unbroken,
    // backslash-separated Windows path with no space to wrap on, alongside
    // the conversation section's real UUID — proving the fix holds
    // independently of which unbreakable token is present.
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
  // The issue's leading hypothesis was a composited-transform/DPI-scale
  // mismatch specific to Windows' WebView2. That did not hold up (the defect
  // reproduces at scale 1 above, with no platform involved), but this still
  // guards the case explicitly so a future scale-dependent regression cannot
  // slip back in unnoticed.
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

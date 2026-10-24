import type { Locator } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// One failure class must render identically across the three Runtime panels;
// only rendering them together can compare them.

// Scoped to the panel so an unrelated alert cannot satisfy it.
function failedReadAlert(panel: Locator): Locator {
  return panel.getByRole('alert').filter({ hasText: 'Cannot read' });
}

test.describe('runtime tab panel failure rendering', () => {
  // The real stub kubectl fails all three probes with the same cause.
  test('one failed read renders the same way in all three panels', async ({ app, seededEnv }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panels = [
      app.manageDialog.runtimeUsagePanel(),
      app.manageDialog.runtimeActivityPanel(),
      app.manageDialog.runtimeSizingPanel(),
    ];

    const renderings: string[] = [];
    for (const panel of panels) {
      await expect(panel).toBeVisible();

      const failure = failedReadAlert(panel);
      await expect(failure).toBeVisible();
      // Never signalled by colour alone.
      await expect(failure.locator('svg')).toHaveCount(1);

      renderings.push((await failure.getAttribute('class')) ?? '');
    }

    expect(renderings[0]).not.toBe('');
    expect(new Set(renderings).size).toBe(1);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

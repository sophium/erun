import type { Locator } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// The Runtime tab's three panels ask three different questions -- how close is
// this environment to its limits, what is it running, what should it be sized
// as -- but they share one failure class: the probe could not read the pod, so
// the panel states why rather than rendering blank.
//
// Each panel used to render that same failed read its own way: amber with a
// warning icon, amber with no icon, and muted grey. Because the message was
// identical in all three, the tone encoded which panel the operator happened to
// be looking at rather than anything about what went wrong.
//
// The existing per-panel cases in manage-runtime-usage.spec.ts,
// manage-runtime-activity.spec.ts and manage-runtime-sizing.spec.ts each assert
// only their own panel's text, which is exactly why none of them caught it: the
// invariant is a comparison between the three, and only rendering them together
// can make it.

// The alert a panel renders for a failed read, scoped to that panel so an
// unrelated alert elsewhere in the tab cannot satisfy it.
function failedReadAlert(panel: Locator): Locator {
  return panel.getByRole('alert').filter({ hasText: 'Cannot read' });
}

test.describe('runtime tab panel failure rendering', () => {
  // The seeded env is inert and never deployed, so the harness's stub kubectl
  // (unmocked here, unlike the stubbed cases in the per-panel specs) fails all
  // three probes for real with the same cause.
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
      // The state is announced (role="alert") and carries an icon as well as a
      // tone, so it is never signalled by colour alone.
      await expect(failure.locator('svg')).toHaveCount(1);

      renderings.push((await failure.getAttribute('class')) ?? '');
    }

    // The point of the issue: one failure class, one rendering. Asserting this
    // per panel would pass while the three still disagreed.
    expect(renderings[0]).not.toBe('');
    expect(new Set(renderings).size).toBe(1);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

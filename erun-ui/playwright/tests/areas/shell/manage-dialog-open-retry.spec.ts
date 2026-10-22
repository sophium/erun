import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Reaching the Manage dialog through a sidebar row must survive a config load
// slower than the open helper's own probe.
//
// pages/Sidebar.ts's openManageDialogViaKeyboard drives the row's edit button
// and probes for the dialog's General tab as one retryable unit, with a short
// inner probe (that method's own comment explains why the probe stays short).
// That retry is structurally dead after its first attempt. Attempt 1's click
// opens the dialog shell; a modal Dialog marks everything outside it
// aria-hidden, so on attempt 2 the helper's own `getByRole` query for the edit
// button resolves to zero elements, and `dispatchEvent` -- a locator action
// with no per-action timeout -- then blocks there for the rest of the test
// rather than retrying. The step gets exactly one probe, so a config load that
// answers at 2.5s reds a test that declared 30s, and reports the attempt it
// never got to make again.
//
// These cases make that deterministic rather than load-dependent: the
// LoadEnvironmentConfig round trip is held for a fixed window, the same
// stubbed-/__erun_invoke shape manage-redeploy-banner.spec.ts uses for its
// slow save, so the failure is on demand on a quiet machine instead of
// waiting for one to be loaded.

// holdInvoke makes `method` unresponsive until the window elapses, then lets
// every pending and subsequent call through. Every call inside the window is
// released at the window's end rather than after a fixed duration, so a poll
// that fires twice cannot slip a fast answer between two slow ones.
async function holdInvoke(page: Page, method: string, holdMs: number): Promise<void> {
  const until = Date.now() + holdMs;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === method) {
      const remaining = until - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
    }
    await route.continue();
  });
}

test.describe('manage dialog open - retry past a slow config load', () => {
  // The reproduction. The dialog shell renders immediately on the click, but
  // ManageDialogTabs returns "Loading config..." until configLoading clears --
  // which happens only when LoadEnvironmentConfig resolves -- so no role=tab
  // exists for the probe to find while the hold is open. 2.5s is just past the
  // helper's 2s probe and far inside the 30s this test declares.
  test('a config load slower than the open probe still converges', async ({ app, page }) => {
    test.setTimeout(30_000);
    await holdInvoke(page, 'LoadEnvironmentConfig', 2_500);

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);

    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.tab('General')).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Why attempt 2 has nothing to click, pinned rather than assumed: while the
  // dialog is up, the row it was opened from is not reachable by role at all.
  // That is the property the retry must survive, and the reason a re-resolving
  // retry has to tolerate a target that is no longer there instead of blocking
  // on it. If a future dialog stops hiding what is behind it this goes red,
  // which is the signal that the tolerance is no longer load-bearing.
  test('an open manage dialog leaves the row it was opened from unreachable by role', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.tab('General')).toBeVisible();

    // environmentRow is the role-based query the helper itself clicks with;
    // envRowButton is the CSS one beside it, which still matches under
    // aria-hidden. The gap between the two is the whole defect: the element is
    // still in the DOM, and only the accessible-name query the helper uses
    // stops resolving to it.
    await expect(app.sidebar.environmentRow(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(0);
    await expect(app.sidebar.envRowButton(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(1);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

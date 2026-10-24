import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Opening the Manage dialog from a sidebar row must survive a config load slower than the open
// helper's probe.
// Once the modal is up the row is aria-hidden, so a retry must tolerate its target being gone.

// Holds every `method` call until the window ends, then releases all at once.
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
  // The tabs render only once LoadEnvironmentConfig resolves; 2.5s is just past the helper's 2s
  // probe.
  test('a config load slower than the open probe still converges', async ({ app, page }) => {
    test.setTimeout(30_000);
    await holdInvoke(page, 'LoadEnvironmentConfig', 2_500);

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);

    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.tab('General')).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Pins why the retry has nothing to click; if this goes red, the tolerance is no longer needed.
  test('an open manage dialog leaves the row it was opened from unreachable by role', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.tab('General')).toBeVisible();

    // environmentRow (role query) stops resolving under aria-hidden while envRowButton (CSS) still
    // matches.
    await expect(app.sidebar.environmentRow(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(0);
    await expect(app.sidebar.envRowButton(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(1);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

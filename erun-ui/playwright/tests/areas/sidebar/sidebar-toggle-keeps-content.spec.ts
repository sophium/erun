import { test, expect } from '../../../fixtures/erunApp.js';

// Regression: toggling the sidebar sometimes blanked the whole content area to
// white until reload. The content region is now wrapped in an ErrorBoundary so
// an uncaught render error surfaces a recoverable fallback instead of a blank
// screen — that recovery is the user-facing contract this spec guards.
//
// The intermittent xterm paint that triggered the blanking can't be reproduced
// deterministically in the headless harness, so this spec locks the reachable
// negative invariant instead of the real trigger.
test.describe('sidebar toggle keeps content rendered', () => {
  test('content pane stays mounted and non-blank across both toggle directions', async ({
    app,
    page,
  }) => {
    const mainPane = page.locator('main').first();
    const errorFallback = page.getByText('Something went wrong', { exact: true });

    await expect(mainPane).toBeVisible();
    await expect(errorFallback).toHaveCount(0);

    await app.titlebar.toggleSidebar();
    await expect.poll(() => readSidebarWidth(page)).toBe('0px');
    await expect(mainPane).toBeVisible();
    await expect(mainPane).not.toBeEmpty();
    await expect(errorFallback).toHaveCount(0);

    await app.titlebar.toggleSidebar();
    await expect.poll(() => readSidebarWidth(page)).not.toBe('0px');
    await expect(mainPane).toBeVisible();
    await expect(mainPane).not.toBeEmpty();
    await expect(errorFallback).toHaveCount(0);
  });
});

async function readSidebarWidth(page: import('@playwright/test').Page): Promise<string> {
  return await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim(),
  );
}

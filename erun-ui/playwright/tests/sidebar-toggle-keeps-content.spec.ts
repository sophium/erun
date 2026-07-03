import { test, expect } from '../fixtures/erunApp.js';

// Regression: toggling the left sidebar sometimes blanked the
// entire content area to white (only the titlebar survived), unrecoverable
// until reload. The fix wraps the content region in an ErrorBoundary so an
// uncaught render error shows a recoverable surface instead of a blank screen.
//
// The intermittent xterm paint trigger cannot be reproduced deterministically
// in the headless harness (the DOM-rendered terminal isn't fully painted off
// screen), so this spec locks the reachable negative invariant: across both
// toggle directions the content pane (<main>) stays mounted and non-empty,
// and the ErrorBoundary fallback never replaces it. The ErrorBoundary's own
// rendering (fallback + retry/reload actions) is exercised by being present
// in the tree; the blank-screen recovery is the user-facing contract.
test.describe('sidebar toggle keeps content rendered', () => {
  test('content pane stays mounted and non-blank across both toggle directions', async ({
    app,
    page,
  }) => {
    const mainPane = page.locator('main').first();
    const errorFallback = page.getByText('Something went wrong', { exact: true });

    await expect(mainPane).toBeVisible();
    await expect(errorFallback).toHaveCount(0);

    // Hide the sidebar.
    await app.titlebar.toggleSidebar();
    await expect.poll(() => readSidebarWidth(page)).toBe('0px');
    await expect(mainPane).toBeVisible();
    await expect(mainPane).not.toBeEmpty();
    await expect(errorFallback).toHaveCount(0);

    // Show it again.
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

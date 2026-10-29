import { test, expect } from '../../../fixtures/erunApp.js';

// The real trigger (an intermittent xterm paint) is not reproducible headless,
// so this pins that toggling keeps content rendered.
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

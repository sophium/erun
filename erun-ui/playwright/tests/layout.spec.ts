import { test, expect } from '../fixtures/erunApp.js';

async function readSidebarWidth(page: import('@playwright/test').Page): Promise<string> {
  return await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim(),
  );
}

test.describe('layout panels', () => {
  test('sidebar toggle flips --sidebar-width', async ({ app, page }) => {
    const initial = await readSidebarWidth(page);
    expect(initial).not.toBe('0px');
    expect(initial.length).toBeGreaterThan(0);

    await app.titlebar.toggleSidebar();
    await expect.poll(() => readSidebarWidth(page)).toBe('0px');

    await app.titlebar.toggleSidebar();
    await expect.poll(() => readSidebarWidth(page)).not.toBe('0px');
  });

  test('review panel toggle reveals the diff section', async ({ app, page }) => {
    const splitter = page.getByRole('button', { name: 'Resize diff panel' });
    const initiallyVisible = await splitter.isVisible().catch(() => false);

    await app.titlebar.toggleReviewPanel();
    await expect.poll(async () => splitter.isVisible().catch(() => false)).toBe(!initiallyVisible);

    // Restore so subsequent tests don't observe an unexpected layout.
    await app.titlebar.toggleReviewPanel();
  });

  test('debug panel toggle reveals the resize handle', async ({ app, page }) => {
    const handle = page.getByRole('button', { name: 'Resize debug panel' });
    const initiallyOpen = await handle.isVisible().catch(() => false);

    await app.debugPanel.toggle();
    await expect.poll(async () => handle.isVisible().catch(() => false)).toBe(!initiallyOpen);

    // Restore prior state.
    await app.debugPanel.toggle();
  });
});

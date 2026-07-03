import { test, expect } from '../fixtures/erunApp.js';

async function readSidebarWidth(page: import('@playwright/test').Page): Promise<string> {
  return await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim(),
  );
}

async function readTerminalCols(page: import('@playwright/test').Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
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

  // Regression: closing the Review panel used to leave the terminal stuck at the
  // narrow cols it shrank to when the panel opened; guard the resize on both edges.
  test('review panel toggle resizes terminal cols on both edges', async ({ app, page }) => {
    await expect.poll(() => readTerminalCols(page)).toBeGreaterThan(0);
    const wideCols = await readTerminalCols(page);

    await app.titlebar.toggleReviewPanel();
    await expect.poll(() => readTerminalCols(page)).toBeLessThan(wideCols);
    const narrowCols = await readTerminalCols(page);
    expect(narrowCols).toBeGreaterThan(0);

    await app.titlebar.toggleReviewPanel();
    // A panel open→close round-trip can settle one column shy of the original from
    // xterm's sub-pixel col rounding; tolerate a 1-col delta since the guard is that
    // the terminal returns to ~wide, not stays stuck near narrow.
    await expect
      .poll(async () => Math.abs((await readTerminalCols(page)) - wideCols))
      .toBeLessThanOrEqual(1);
  });

  test('diagnostics panel toggle reveals the resize handle', async ({ app }) => {
    const handle = app.debugPanel.resizeHandle();
    const initiallyOpen = await handle.isVisible().catch(() => false);

    await app.debugPanel.toggle();
    await expect.poll(async () => handle.isVisible().catch(() => false)).toBe(!initiallyOpen);

    // Restore prior state.
    await app.debugPanel.toggle();
  });
});

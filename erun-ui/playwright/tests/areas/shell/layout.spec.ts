import { test, expect } from '../../../fixtures/erunApp.js';

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
    await page.waitForFunction(
      () =>
        getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim() ===
        '0px',
    );
    await expect.poll(() => readSidebarWidth(page)).toBe('0px');

    await app.titlebar.toggleSidebar();
    await page.waitForFunction(
      () =>
        getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim() !==
        '0px',
    );
    await expect.poll(() => readSidebarWidth(page)).not.toBe('0px');
  });

  test('review panel toggle reveals the diff section', async ({ app, page }) => {
    const splitter = page.getByRole('slider', { name: 'Resize diff panel' });
    const initiallyVisible = await splitter.isVisible().catch(() => false);

    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'hidden' : 'visible' });
    await expect(splitter).toBeVisible({ visible: !initiallyVisible });

    // Restore, converged so a slow restore can't leak into the next test.
    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'visible' : 'hidden' });
  });

  test('review panel toggle resizes terminal cols on both edges', async ({ app, page }) => {
    await expect.poll(() => readTerminalCols(page)).toBeGreaterThan(0);
    const wideCols = await readTerminalCols(page);

    await app.titlebar.toggleReviewPanel();
    await page.waitForFunction(
      (wide) =>
        Number.parseInt(
          document.querySelector<HTMLElement>('.terminal')?.dataset.terminalCols ?? '0',
          10,
        ) < wide,
      wideCols,
    );
    const narrowCols = await readTerminalCols(page);
    expect(narrowCols).toBeGreaterThan(0);

    await app.titlebar.toggleReviewPanel();
    // xterm's sub-pixel rounding can settle one column shy after the round-trip.
    await page.waitForFunction(
      (wide) =>
        Math.abs(
          Number.parseInt(
            document.querySelector<HTMLElement>('.terminal')?.dataset.terminalCols ?? '0',
            10,
          ) - wide,
        ) <= 1,
      wideCols,
    );
  });

  test('diagnostics panel toggle reveals the resize handle', async ({ app }) => {
    const handle = app.debugPanel.resizeHandle();
    const initiallyOpen = await handle.isVisible().catch(() => false);

    await app.debugPanel.toggle();
    await handle.waitFor({ state: initiallyOpen ? 'hidden' : 'visible' });
    await expect(handle).toBeVisible({ visible: !initiallyOpen });

    await app.debugPanel.toggle();
    await handle.waitFor({ state: initiallyOpen ? 'visible' : 'hidden' });
  });
});

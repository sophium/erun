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

    // waitForFunction (not expect.poll) converges against the enclosing test's
    // own budget -- the collapsed width is a CSS custom property with no
    // locator to wait on.
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

    // waitFor (not expect.poll) converges against the enclosing test's own
    // budget instead of racing the toggle's render against expect's fixed one
    // -- the same swap the smoke suite's equivalent test made.
    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'hidden' : 'visible' });
    await expect(splitter).toBeVisible({ visible: !initiallyVisible });

    // Restore so subsequent tests don't observe an unexpected layout --
    // converge on that too, so a slow restore can't leak into the next test.
    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'visible' : 'hidden' });
  });

  // Regression: closing the Review panel used to leave the terminal stuck at the
  // narrow cols it shrank to when the panel opened; guard the resize on both edges.
  test('review panel toggle resizes terminal cols on both edges', async ({ app, page }) => {
    await expect.poll(() => readTerminalCols(page)).toBeGreaterThan(0);
    const wideCols = await readTerminalCols(page);

    // waitForFunction (not expect.poll) converges against the enclosing test's
    // own budget -- terminal cols are a DOM value with no locator to wait on,
    // so this is the same equivalent the smoke suite's class-attribute test
    // uses for its own non-locator state.
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
    // A panel open→close round-trip can settle one column shy of the original from
    // xterm's sub-pixel col rounding; tolerate a 1-col delta since the guard is that
    // the terminal returns to ~wide, not stays stuck near narrow. Converge on it
    // for the same reason as the open above.
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

    // Converge through the panel's own open/closed helper rather than racing
    // the toggle's render against expect's fixed budget.
    await app.debugPanel.toggle();
    await handle.waitFor({ state: initiallyOpen ? 'hidden' : 'visible' });
    await expect(handle).toBeVisible({ visible: !initiallyOpen });

    // Restore prior state, converged so a slow restore can't leak.
    await app.debugPanel.toggle();
    await handle.waitFor({ state: initiallyOpen ? 'visible' : 'hidden' });
  });
});

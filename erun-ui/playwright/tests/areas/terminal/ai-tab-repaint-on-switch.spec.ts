import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// A main-screen TUI session must repaint when its tab is re-selected. The seed's
// AI tool is `sh`, so this proves the mechanism, not a real claude UI.
test.describe('AI tab repaints on switch', () => {
  test('switching back to the AI tab shows its content, not a blank pane', async ({
    app,
    page,
    seededEnv,
  }, testInfo) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const aiTab = app.tabStrip.tab('AI');
    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('AI');
    await app.tabStrip.waitForTab('Local');

    await aiTab.click();
    await expect.poll(() => rowsText(page), { timeout: 20_000 }).not.toBe('');

    await localTab.click();
    await expect.poll(() => rowsText(page), { timeout: 10_000 }).not.toBe('');
    await aiTab.click();

    await expect.poll(() => rowsText(page), { timeout: 10_000 }).not.toBe('');
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    const shot = testInfo.outputPath('ai-tab-after-switch.png');
    await page.screenshot({ path: shot });
    await testInfo.attach('ai-tab-after-switch', { path: shot, contentType: 'image/png' });
  });

  // The AI tab must show content on first open without a keypress; the backend
  // repaint nudge is Go-tested.
  test('the AI tab shows its content on first open without a keypress', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const aiTab = app.tabStrip.tab('AI');
    await app.tabStrip.waitForTab('AI');

    await aiTab.click();
    await expect.poll(() => rowsText(page), { timeout: 20_000 }).not.toBe('');
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
  });
});

function rowsText(page: Page): Promise<string> {
  return page.evaluate(() =>
    (document.querySelector('.xterm-rows')?.textContent ?? '').replace(/\s+/g, ''),
  );
}

async function terminalAtBottom(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const v = document.querySelector<HTMLElement>('.xterm-viewport');
    if (!v) return false;
    return v.scrollTop >= v.scrollHeight - v.clientHeight - 2;
  });
}

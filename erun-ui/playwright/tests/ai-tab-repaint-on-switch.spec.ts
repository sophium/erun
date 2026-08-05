import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Regression guard for the "AI tab is black on switch" bug: a main-screen TUI
// session (the seeded AI tab runs an inert `sh`) must repaint its content when
// its tab is re-selected, rather than showing a blank pane that only fills in
// once the user types. Harness note: the seed's AI tool is `sh`, so this proves
// the switch/repaint MECHANISM renders a main-screen session's content — it does
// not (and cannot here) render a real claude UI.
test.describe('AI tab repaints on switch', () => {
  test('switching back to the AI tab shows its content, not a blank pane', async ({
    app,
    page,
    seededEnv,
  }, testInfo) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await aiTab.waitFor({ state: 'visible', timeout: 20_000 });
    await localTab.waitFor({ state: 'visible', timeout: 20_000 });

    // Land on the AI tab and wait for the inert shell to render its prompt.
    await aiTab.click();
    await expect.poll(() => rowsText(page), { timeout: 20_000 }).not.toBe('');

    // Switch away and back — the path the repaint fix covers.
    await localTab.click();
    await expect.poll(() => rowsText(page), { timeout: 10_000 }).not.toBe('');
    await aiTab.click();

    // The fix: a main-screen session repaints on switch, so its content is
    // present immediately and the viewport sits at the live prompt.
    await expect.poll(() => rowsText(page), { timeout: 10_000 }).not.toBe('');
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    const shot = testInfo.outputPath('ai-tab-after-switch.png');
    await page.screenshot({ path: shot });
    await testInfo.attach('ai-tab-after-switch', { path: shot, contentType: 'image/png' });
  });

  // Initial open (issue #861): the AI tab must show its content on first open,
  // without the user typing. The seed's AI tool is an inert `sh`, so this guards
  // the observable invariant — content present and the viewport at the prompt on
  // first open — but cannot render a real Claude UI. The backend repaint nudge
  // that fixes the real reattach (split-chunk attach-marker detection + bounded
  // retry) is covered by the Go unit tests in erun-ui/app_test.go
  // (TestAISessionRepaintNudge*).
  test('the AI tab shows its content on first open without a keypress', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
    await aiTab.waitFor({ state: 'visible', timeout: 20_000 });

    // First open only — no switch, no input. Content must appear on its own.
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

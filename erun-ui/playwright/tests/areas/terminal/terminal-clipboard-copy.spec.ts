import { expect, test } from '../../../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';
import { captureInvokes, sessionInputs } from '../../../pages/index.js';

// Selected text must reach the clipboard via the platform's chords (Cmd+C on
// macOS, chosen by user agent). Chromium is not WKWebView, so native Cmd+V and
// keydown delivery are not proven here.

const MAC_USER_AGENT =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';

const INTERRUPT = '\x03';
const ESCAPE = '\x1b';

// Waits for the harness prompt first so shell startup output is behind the
// clear; the shell may still redraw, so printOnlyLine leaves row 0.
async function selectPrintedLine(app: AppShell, sessionId: number, marker: string): Promise<void> {
  await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);
  await app.terminalPane.printOnlyLine(sessionId, marker);
  await expect(app.terminalPane.rows()).toContainText(marker);
  await app.terminalPane.selectFirstRow();
}

test.describe('terminal selection copy on macOS (#969)', () => {
  test.use({ userAgent: MAC_USER_AGENT });

  test('Cmd+C puts the selection on the host clipboard', async ({ app, page, seededEnv }) => {
    const marker = 'erun-copy-mac-authorize-url';
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-copy');

    await selectPrintedLine(app, sessionId, marker);
    await page.keyboard.press('Meta+c');

    await expect.poll(() => app.terminalPane.hostClipboard()).toContain(marker);
  });

  // On macOS Ctrl+C is the interrupt; a selection must never swallow it.
  test('Ctrl+C still reaches the session as ^C even with a selection', async ({
    app,
    page,
    seededEnv,
  }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-interrupt');

    await selectPrintedLine(app, sessionId, 'erun-copy-mac-interrupt');
    const invokes = captureInvokes(page);
    await page.keyboard.press('Control+c');

    await expect.poll(() => sessionInputs(invokes, sessionId)).toContain(INTERRUPT);
    expect(await app.terminalPane.hostClipboard()).toBe('clipboard-before-interrupt');
  });
});

test.describe('terminal selection copy on Windows/Linux (#969)', () => {
  test('Ctrl+C puts the selection on the host clipboard', async ({ app, page, seededEnv }) => {
    const marker = 'erun-copy-other-authorize-url';
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-copy');

    await selectPrintedLine(app, sessionId, marker);
    await page.keyboard.press('Control+c');

    await expect.poll(() => app.terminalPane.hostClipboard()).toContain(marker);
  });

  test('Ctrl+C with no selection reaches the session as ^C', async ({ app, page, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-interrupt');

    await app.terminalPane.printOnlyLine(sessionId, 'erun-copy-other-no-selection');
    await expect(app.terminalPane.rows()).toContainText('erun-copy-other-no-selection');
    await app.terminalPane.screen().click();

    const invokes = captureInvokes(page);
    await page.keyboard.press('Control+c');

    await expect.poll(() => sessionInputs(invokes, sessionId)).toContain(INTERRUPT);
    expect(await app.terminalPane.hostClipboard()).toBe('clipboard-before-interrupt');
  });

  // The platform chord tables must stay apart.
  test('Cmd+C does not copy off macOS', async ({ app, page, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-meta');

    await selectPrintedLine(app, sessionId, 'erun-copy-other-meta-ignored');
    const invokes = captureInvokes(page);
    await page.keyboard.press('Meta+c');

    // Escape passes straight to the session, so once it lands any Cmd+C write
    // would already have happened.
    await page.keyboard.press('Escape');
    await expect.poll(() => sessionInputs(invokes, sessionId)).toContain(ESCAPE);
    expect(await app.terminalPane.hostClipboard()).toBe('clipboard-before-meta');
  });
});

import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';
import { captureInvokes, sessionInputs } from '../pages/index.js';

// #969: text selected in a desktop terminal tab never reached the macOS
// clipboard. The chord table was Windows/Linux-shaped and never inspected
// metaKey, so Cmd+C reduced to a bare "c" and the copy path was unreachable on
// the platform the desktop ships to first.
//
// These specs drive the real chord layer through a real xterm selection and
// assert the observable outcome — what is on the host clipboard — rather than
// that a handler is registered. The macOS block sets the browser's user agent,
// which is the same signal the production build reads (the WebView UA) to pick
// the platform's chords.
//
// Harness limitation: Playwright's Chromium is not the macOS WKWebView the
// desktop embeds. This proves the chord classification and the copy path behind
// it; it cannot prove WKWebView delivers Cmd+C as a keydown carrying metaKey,
// nor that it leaves Cmd+V's native paste event intact (the reason Cmd+V is
// deliberately not intercepted). Those need a real macOS desktop. The
// per-platform chord tables are unit-covered in frontend/src/app/clipboard.test.ts.

const MAC_USER_AGENT =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';

const INTERRUPT = '\x03';
const ESCAPE = '\x1b';

// Prints one known line and selects it with the mouse, leaving the terminal in
// the state an operator is in when they have highlighted a URL the pod printed.
//
// The Local tab's real shell prints its own prompt asynchronously once it has
// started, over the same event channel printOnlyLine's synthetic clear uses —
// so this waits for that known, harness-controlled prompt (see
// fixtures/seedRoot.ts ERUN_LOCAL_SHELL_OVERRIDE) first. Only once the shell
// has gone quiet is it safe to clear the screen and print the marker: nothing
// else the shell does on its own can still land on row 0 afterward.
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

  // On macOS Ctrl+C is the interrupt, not a copy chord — a stale selection must
  // never swallow it.
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

  // The guard the copy path must not lose: with nothing selected, Ctrl+C is the
  // interrupt and has to reach the session.
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

  // Cmd is not a Windows/Linux clipboard modifier, and the pre-fix chord
  // signature ignored meta entirely — so this is the case proving the two
  // platform tables stay apart rather than both matching everything.
  test('Cmd+C does not copy off macOS', async ({ app, page, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    expect(sessionId).toBeGreaterThan(0);
    await app.terminalPane.setHostClipboard('clipboard-before-meta');

    await selectPrintedLine(app, sessionId, 'erun-copy-other-meta-ignored');
    const invokes = captureInvokes(page);
    await page.keyboard.press('Meta+c');

    // Bound the negative on a real round-trip rather than a delay: Escape is no
    // clipboard chord on any platform, so it falls straight through to the
    // session, and by the time it lands any write Cmd+C would have made has
    // already happened.
    await page.keyboard.press('Escape');
    await expect.poll(() => sessionInputs(invokes, sessionId)).toContain(ESCAPE);
    expect(await app.terminalPane.hostClipboard()).toBe('clipboard-before-meta');
  });
});

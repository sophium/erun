import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// xterm's Linkifier tracks the last hovered buffer cell and treats a
// mousemove into that same cell as a no-op, so it never re-asks its link
// providers -- not even after a real mouseleave-then-mouseenter back onto the
// exact same screen position. A user who hovers a link, glances away, and
// returns to the identical pixel (an entirely ordinary interaction) gets no
// hover re-evaluation at all, so a link that was overwritten with different
// content while they were away is never re-resolved: the cursor-pointer
// decoration silently never reappears, and the stale link can never be
// clicked again either way -- neither the old target nor the new one.
async function printLine(app: AppShell, sessionId: number, text: string): Promise<void> {
  await app.terminalPane.printOnlyLine(sessionId, text);
  await expect(app.terminalPane.rows()).toContainText(text);
}

// Mirrors terminal-clickable-links.spec.ts's hoverAndClickDecoratedLink: a
// single hover can be dropped under load, so the wait-for-decoration retries,
// re-hovering each attempt.
async function hoverAndWaitForDecoration(app: AppShell): Promise<void> {
  await expect(async () => {
    await app.terminalPane.hoverFirstRow();
    await expect(app.terminalPane.screen()).toHaveClass(/xterm-cursor-pointer/, {
      timeout: 2_000,
    });
  }).toPass({ timeout: 10_000 });
}

test.describe('terminal link hover survives an in-place overwrite (#2149)', () => {
  test('a link overwritten while hovered re-resolves after leaving and returning', async ({
    app,
    page,
    seededEnv,
  }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);

    const urlA = 'https://example.com/erun-cache-a';
    const urlB = 'https://example.com/erun-cache-b';

    // Hover A -- the decoration appears, proving the pointer sits over a
    // real, resolved link.
    await printLine(app, sessionId, urlA);
    await hoverAndWaitForDecoration(app);

    // Clear the screen and reprint a DIFFERENT link at the exact same
    // row/column, without moving the pointer.
    await printLine(app, sessionId, urlB);

    // Leave the terminal and return to the identical coordinates -- an
    // entirely ordinary user interaction (glancing away, then back, before
    // clicking).
    await page.mouse.move(0, 0);
    await hoverAndWaitForDecoration(app);

    // The decoration must reflect B, the link actually under the pointer
    // now -- not A (stale), and not "nothing" (the observed failure mode:
    // the decoration never reappearing at all).
    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      app.terminalPane.clickFirstRow(),
    ]);
    await expect.poll(() => popup.url()).toBe(urlB);
    await popup.close();
  });
});

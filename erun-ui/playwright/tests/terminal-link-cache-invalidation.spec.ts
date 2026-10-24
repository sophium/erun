import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// xterm's Linkifier ignores a mousemove into the last hovered cell, so a link
// overwritten in place must still re-resolve after leave-and-return.
async function printLine(app: AppShell, sessionId: number, text: string): Promise<void> {
  await app.terminalPane.printOnlyLine(sessionId, text);
  await expect(app.terminalPane.rows()).toContainText(text);
}

// A single hover can be dropped under load, so retry with a re-hover.
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

    await printLine(app, sessionId, urlA);
    await hoverAndWaitForDecoration(app);

    // Reprint a different link at the same position without moving the pointer.
    await printLine(app, sessionId, urlB);

    await page.mouse.move(0, 0);
    await hoverAndWaitForDecoration(app);

    // Must open B, not stale A and not nothing.
    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      app.terminalPane.clickFirstRow(),
    ]);
    await expect.poll(() => popup.url()).toBe(urlB);
    await popup.close();
  });
});

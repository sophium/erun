import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// #2149 repro probe (temporary): hover a link, overwrite the same screen
// position with a DIFFERENT link via a clear+reprint (the same primitive
// TerminalPane.printOnlyLine already uses), then move off and back onto the
// same coordinates and see what xterm reports as hovered.
async function printLine(app: AppShell, sessionId: number, text: string): Promise<void> {
  await app.terminalPane.printOnlyLine(sessionId, text);
  await expect(app.terminalPane.rows()).toContainText(text);
}

test('probe: does the decoration survive an in-place link overwrite', async ({
  app,
  page,
  seededEnv,
}) => {
  const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
  await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);

  const urlA = 'https://example.com/erun-cache-a';
  const urlB = 'https://example.com/erun-cache-b';

  await printLine(app, sessionId, urlA);
  await app.terminalPane.hoverFirstRow();
  await expect(app.terminalPane.screen()).toHaveClass(/xterm-cursor-pointer/, { timeout: 5_000 });
  console.log('STEP1: decoration present for A');

  await printLine(app, sessionId, urlB);
  console.log('STEP2: printed B without moving mouse, waiting 1s to observe');
  await page.waitForTimeout(1000);
  const hasDecorationAfterOverwriteNoMove = await app.terminalPane
    .screen()
    .evaluate((el) => el.classList.contains('xterm-cursor-pointer'));
  console.log(
    'STEP2 result: decoration present without moving mouse?',
    hasDecorationAfterOverwriteNoMove,
  );

  if (hasDecorationAfterOverwriteNoMove) {
    const [popupStep2] = await Promise.all([
      page.waitForEvent('popup', { timeout: 5_000 }).catch(() => null),
      app.terminalPane.clickFirstRow(),
    ]);
    console.log(
      'STEP2b result: popup url after clicking without moving mouse =',
      popupStep2?.url() ?? '(no popup)',
    );
    if (popupStep2) {
      await popupStep2.close();
    }
    // Re-hover so the click above (which may have moved focus/state) leaves
    // us back in a known hovering state before step 3.
    await app.terminalPane.hoverFirstRow();
  }

  // Move off and back onto the same coordinates.
  await page.mouse.move(0, 0);
  await app.terminalPane.hoverFirstRow();
  await page.waitForTimeout(1000);
  const hasDecorationAfterMoveAwayAndBack = await app.terminalPane
    .screen()
    .evaluate((el) => el.classList.contains('xterm-cursor-pointer'));
  console.log(
    'STEP3 result: decoration present after move-away-and-back?',
    hasDecorationAfterMoveAwayAndBack,
  );

  const [popup] = await Promise.all([
    page.waitForEvent('popup', { timeout: 5_000 }).catch(() => null),
    app.terminalPane.clickFirstRow(),
  ]);
  console.log('STEP4 result: popup url after click =', popup?.url() ?? '(no popup)');
  if (popup) {
    await popup.close();
  }
});

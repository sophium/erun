import { expect, test } from '../../../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Some shells (macOS bash 3.2) redraw the current line on SIGWINCH, which can
// erase a printed row 0. The redraw is emitted directly with a sentinel prompt
// and ends on the next row, so a later real redraw cannot erase the sentinel.
const REDRAW_PROMPT = 'erun-redraw-sentinel$ ';
const READLINE_RESIZE_REDRAW = `\r\x1b[K${REDRAW_PROMPT}\r\n`;

test('a printed line survives the shell redrawing its line after a resize', async ({
  app,
  seededEnv,
}) => {
  const marker = 'erun-printed-line-survives-redraw';
  const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
  await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);

  await app.terminalPane.printOnlyLine(sessionId, marker);
  await expect(app.terminalPane.rows()).toContainText(marker);
  await app.terminalPane.emitOutput(sessionId, READLINE_RESIZE_REDRAW);

  await expect(app.terminalPane.rows()).toContainText(REDRAW_PROMPT);
  await expect(app.terminalPane.firstRow()).toContainText(marker);
});

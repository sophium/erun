import { expect, test } from '../../../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The terminal specs print a known line at the top of a cleared screen and then
// hover, click or select row 0. The pane is backed by a live shell, and a shell
// with line editing redraws its current line when it is resized: macOS's
// /bin/sh (bash 3.2, readline) writes "\r\x1b[K" plus its prompt on SIGWINCH,
// while Linux's dash writes nothing. So a printed line that such a redraw can
// erase reds every row-0 spec on macOS and none on Linux. Emitting the redraw
// directly makes that state reachable on every host. Its prompt is a sentinel
// rather than the real one, so seeing it on screen proves this redraw -- not
// one the live shell sent on its own -- has landed.
const REDRAW_PROMPT = 'erun-redraw-sentinel$ ';
const READLINE_RESIZE_REDRAW = `\r\x1b[K${REDRAW_PROMPT}`;

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

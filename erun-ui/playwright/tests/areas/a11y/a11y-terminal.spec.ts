import AxeBuilder from '@axe-core/playwright';

import { expect, test } from '../../../fixtures/erunApp.js';

// The terminal host had no aria-label/role and xterm's screenReaderMode
// was never turned on, so the AccessibilityManager live region that carries
// output to a screen reader was never created -- every `erun` command, and
// every AI agent turn, was silent. These specs lock the observable proof of
// both fixes: the accessibility tree only exists when screenReaderMode is on,
// and the host div is reachable/named for a user who lands on it.

test.describe('terminal accessibility', () => {
  test('the terminal host is a named, reachable group', async ({ app }) => {
    const host = app.terminalPane.host();
    await expect(host).toBeVisible();
    await expect(host).toHaveAttribute('role', 'group');
    await expect(host).toHaveAccessibleName('Terminal');
  });

  // #1335: screenReaderMode must be OFF unless asked for. xterm's
  // AccessibilityManager rewrites the same hidden helper textarea that captures
  // keystrokes, so with it on a focus transition mid-line made the next input
  // event re-emit the whole accumulated buffer -- the operator's sentence
  // arrived as several concatenated, progressively longer copies of itself.
  // The absence of the tree by default IS the guard: this fails against the
  // hardcoded `screenReaderMode: true` #1291 shipped.
  test('the screen-reader accessibility tree is absent by default', async ({ app }) => {
    await expect(app.terminalPane.accessibilityTree()).toHaveCount(0);
  });

  test('xterm builds its screen-reader accessibility tree when opted in', async ({ app, page }) => {
    await page.evaluate(() => {
      window.localStorage.setItem('erun.terminal.screenReaderMode', 'true');
    });
    await page.reload();
    // Only present when `screenReaderMode: true` reached `new Terminal(...)`
    // -- xterm's AccessibilityManager is not instantiated otherwise, so this
    // element's mere existence is the regression guard for the option.
    await expect(app.terminalPane.accessibilityTree()).toHaveCount(1);
    await expect(app.terminalPane.accessibilityTree().locator('[role="list"]')).toHaveCount(1);
  });

  // Scoped to the host div itself, not a whole-page scan: axe can't observe
  // xterm's screen-reader wiring (checked directly above), but it can catch a
  // structurally broken landmark/name on the surface we changed.
  test('axe reports no violations for the terminal host', async ({ app, page }) => {
    await expect(app.terminalPane.host()).toBeVisible();
    const results = await new AxeBuilder({ page }).include('#erun-terminal-pane').analyze();
    expect(results.violations).toEqual([]);
  });
});

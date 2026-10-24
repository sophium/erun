import AxeBuilder from '@axe-core/playwright';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

test.describe('terminal accessibility', () => {
  test('the terminal host is a named, reachable group', async ({ app }) => {
    const host = app.terminalPane.host();
    await host.waitFor({ state: 'visible' });
    await expect(host).toBeVisible();
    await expect(host).toHaveAttribute('role', 'group');
    await expect(host).toHaveAccessibleName('Terminal');
  });

  // With screenReaderMode on, a focus change mid-line makes the next input re-emit
  // the whole buffer, so it must be off by default.
  test('the screen-reader accessibility tree is absent by default', async ({ app }) => {
    await expect(app.terminalPane.accessibilityTree()).toHaveCount(0);
  });

  test('xterm builds its screen-reader accessibility tree when opted in', async ({ app, page }) => {
    await page.evaluate(() => {
      window.localStorage.setItem('erun.terminal.screenReaderMode', 'true');
    });
    await page.reload();
    await expect(app.terminalPane.accessibilityTree()).toHaveCount(1, withTestBudget());
    await expect(app.terminalPane.accessibilityTree().locator('[role="list"]')).toHaveCount(
      1,
      withTestBudget(),
    );
  });

  // Scoped to the host div; axe can't observe xterm's screen-reader wiring.
  test('axe reports no violations for the terminal host', async ({ app, page }) => {
    await app.terminalPane.host().waitFor({ state: 'visible' });
    await expect(app.terminalPane.host()).toBeVisible();
    const results = await new AxeBuilder({ page }).include('#erun-terminal-pane').analyze();
    expect(results.violations).toEqual([]);
  });
});

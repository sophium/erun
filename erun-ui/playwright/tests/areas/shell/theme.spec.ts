import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Covers the class-based `.dark` toggle set by applyTheme/initialTheme (app/theme.ts).

test.describe('theme', () => {
  test.describe('dark OS preference, no stored choice', () => {
    test.use({ colorScheme: 'dark' });

    test('the dark class is already applied at domcontentloaded', async ({ page }) => {
      // Assert at domcontentloaded so a class applied late (after first paint) is caught.
      await page.goto('/');
      await page.waitForLoadState('domcontentloaded');
      await expect(page.locator('html')).toHaveClass(/dark/);
    });

    test('the titlebar control switches to light and the choice persists', async ({ app }) => {
      await expect(app.documentElement()).toHaveClass(/dark/);
      const toggle = app.titlebar.themeToggleButton();
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to light theme');

      await app.titlebar.toggleTheme();
      await expect(app.documentElement()).not.toHaveClass(/dark/, withTestBudget());
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to dark theme');

      // The OS still prefers dark, so only a persisted choice keeps the relaunch light.
      await app.open();
      await expect(app.documentElement()).not.toHaveClass(/dark/, withTestBudget());
    });

    test('the sidebar and terminal pane stay legible and reachable', async ({ app }) => {
      await expect(app.documentElement()).toHaveClass(/dark/);
      await expect(app.sidebar.envRowButton(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();
      await app.openEnvironmentTerminal(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(app.terminalPane.screen()).toBeVisible();
    });
  });

  test.describe('light OS preference, no stored choice', () => {
    test.use({ colorScheme: 'light' });

    test('the dark class is not applied', async ({ app }) => {
      await expect(app.documentElement()).not.toHaveClass(/dark/);
    });

    test('the titlebar control is reachable and toggles to dark', async ({ app }) => {
      const toggle = app.titlebar.themeToggleButton();
      await expect(toggle).toBeVisible();
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to dark theme');

      await app.titlebar.toggleTheme();
      await expect(app.documentElement()).toHaveClass(/dark/, withTestBudget());
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to light theme');
    });
  });
});

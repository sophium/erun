import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// theme covers the class-based `.dark` toggle (#1356). Before this, erun-ui
// shipped 29 `dark:` Tailwind utilities but nothing ever set the gating
// class, so every one of them was dead code and an operator had no way to
// reach a dark theme at all. This spec is the red-then-green proof: on
// `main` the app never carries the `dark` class under any OS preference, so
// every assertion below that expects it fails there and only passes once
// `applyTheme`/`initialTheme` (app/theme.ts) are wired in.

test.describe('theme', () => {
  test.describe('dark OS preference, no stored choice', () => {
    test.use({ colorScheme: 'dark' });

    test('the dark class is already applied at domcontentloaded', async ({ page }) => {
      // domcontentloaded is the earliest point a spec can observe: main.tsx's
      // synchronous applyTheme(initialTheme()) call (module scripts complete
      // before this event) has already run, but well before this spec's own
      // AppShell.open() would otherwise wait out the boot sequence. Asserting
      // here — not after the app finishes booting — is what would catch a
      // class applied late from a useEffect instead of before first paint.
      await page.goto('/');
      await page.waitForLoadState('domcontentloaded');
      await expect(page.locator('html')).toHaveClass(/dark/);
    });

    test('the titlebar control switches to light and the choice persists', async ({ app }) => {
      await expect(app.documentElement()).toHaveClass(/dark/);
      const toggle = app.titlebar.themeToggleButton();
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to light theme');

      await app.titlebar.toggleTheme();
      await expect(app.documentElement()).not.toHaveClass(/dark/);
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to dark theme');

      // The OS still prefers dark, so only a persisted explicit choice can
      // keep a relaunch light.
      await app.open();
      await expect(app.documentElement()).not.toHaveClass(/dark/);
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
      await expect(app.documentElement()).toHaveClass(/dark/);
      await expect(toggle).toHaveAttribute('aria-label', 'Switch to light theme');
    });
  });
});

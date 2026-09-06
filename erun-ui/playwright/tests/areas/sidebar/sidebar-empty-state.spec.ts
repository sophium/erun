import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// erun#1217: the zero-tenant empty state promised a kubeconfig-import feature
// that does not exist anywhere in the app (no affordance, no Wails binding,
// no CLI command — a repo-wide grep for kubeconfig import returns exactly
// this one sentence), and the sidebar's "+" was labelled "...remote
// environment" for a dialog that offers three env types. Stub a bare
// LoadState response (zero tenants) rather than the AppShell fixture's
// seeded baseline, which always carries tenant `pw` — so the route must be
// registered before the app's own boot navigation, not through the `app`
// fixture (which opens immediately).
async function stubEmptyState(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: { tenants: [] } }),
      });
    }
    await route.continue();
  });
}

test.describe('sidebar empty state (#1217)', () => {
  test('names no feature that does not exist and offers all env types', async ({ page }) => {
    await stubEmptyState(page);
    const app = new AppShell(page);
    await app.open();

    const emptyState = page.getByText('No environments yet').locator('..');
    await expect(emptyState).toBeVisible();
    await expect(emptyState).not.toContainText(/kubeconfig/i);
    await expect(emptyState).not.toContainText(/remote environment/i);
  });

  test('the header "+" is labelled for any environment type, not just remote', async ({ app }) => {
    // Reachable from the seeded baseline: this icon button renders in the
    // Environments header regardless of tenant count.
    await expect(
      app.page.getByRole('button', { name: 'Initialize new environment' }),
    ).toBeVisible();
    await expect(
      app.page.getByRole('button', { name: 'Initialize new remote environment' }),
    ).toHaveCount(0);
  });
});

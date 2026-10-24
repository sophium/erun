import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// Stubs a zero-tenant LoadState; registered before boot because the `app` fixture's seed always has
// tenant `pw`.
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
    await expect(
      app.page.getByRole('button', { name: 'Initialize new environment' }),
    ).toBeVisible();
    await expect(
      app.page.getByRole('button', { name: 'Initialize new remote environment' }),
    ).toHaveCount(0);
  });
});

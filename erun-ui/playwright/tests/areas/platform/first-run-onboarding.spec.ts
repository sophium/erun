import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// A fresh install reports ErrNotInitialized with a null tenants list; it must
// render the same empty state as zero tenants.
async function stubFreshInstallState(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: { tenants: null } }),
      });
    }
    await route.continue();
  });
}

test.describe('first-run onboarding (#1396)', () => {
  test('a genuinely fresh install renders the actionable empty state, never a crash or a bare CLI instruction', async ({
    page,
  }) => {
    await stubFreshInstallState(page);
    const app = new AppShell(page);
    await app.open();

    await expect(page.getByText('Something went wrong')).toHaveCount(0);

    const emptyState = page.getByText('No environments yet').locator('..');
    await expect(emptyState).toBeVisible();
    await expect(page.getByRole('button', { name: 'Initialize new environment' })).toBeVisible();

    await expect(page.getByText(/erun init/i)).toHaveCount(0);
  });
});

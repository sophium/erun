import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// The sidebar must not show the "No environments yet" empty state before LoadState returns; gate
// the response to observe the loading window.
async function stubGatedLoadState(page: Page): Promise<() => void> {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'LoadState') {
      await route.continue();
      return;
    }
    await gate;
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: { tenants: [] } }),
    });
  });
  return release;
}

test.describe('sidebar loading state (#2377)', () => {
  test('does not claim "No environments yet" while the tenant list is still loading', async ({
    page,
  }) => {
    const release = await stubGatedLoadState(page);
    const app = new AppShell(page);
    // reboot() returns before LoadState resolves; open() would wait out the race.
    await app.reboot();

    await expect(page.getByText('No environments yet')).toHaveCount(0);
    await expect(page.getByText('Loading environments…', { exact: true })).toBeVisible();

    release();

    await expect(page.getByText('Loading environments…')).toHaveCount(0);
    await expect(page.getByText('No environments yet')).toBeVisible();
  });
});

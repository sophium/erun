import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { AppShell } from '../../../pages/index.js';

// erun#2377: the sidebar rendered the confident "No environments yet" empty
// state (with its "Initialize environment" action) before the tenant list
// had come back at all, contradicting the main pane's own "Loading
// environments..." message shown in the same frame. Gate the LoadState
// response so the assertions can observe the still-loading window before
// releasing it, the way a real cold start's network latency would.
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
    // reboot() hands back control as soon as the app chrome is up, before
    // LoadState resolves — open() would wait out the very race this test
    // needs to observe.
    await app.reboot();

    await expect(page.getByText('No environments yet')).toHaveCount(0);
    await expect(page.getByText('Loading environments…', { exact: true })).toBeVisible();

    release();

    await expect(page.getByText('Loading environments…')).toHaveCount(0);
    await expect(page.getByText('No environments yet')).toBeVisible();
  });
});

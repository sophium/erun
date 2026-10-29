import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// An erun alias referenced by no tenant must still appear in the sidebar. LoadState
// is stubbed because adding an unattached alias to the shared seed changes other specs.

const TENANT = 'unattached-erun';
const UNATTACHED_ERUN_ALIAS = 'erun+api.acme.test@erun';

function loadStateWithUnattachedERunAlias(): unknown {
  return {
    tenants: [{ name: TENANT, environments: [] }],
    cloudProviders: [
      {
        alias: UNATTACHED_ERUN_ALIAS,
        provider: 'erun',
        status: 'active',
        username: 'erun',
        accountId: 'api.acme.test',
      },
    ],
    build: { version: '0.0.0-test' },
  };
}

async function stubLoadState(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'LoadState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: loadStateWithUnattachedERunAlias() }),
      });
    }
    await route.continue();
  });
}

test.describe('sidebar surfaces an erun alias no tenant attaches (#1914)', () => {
  test('an unattached erun alias renders its own sidebar row', async ({ app, page }) => {
    await stubLoadState(page);
    await app.reboot();

    await expect(app.sidebar.cloudAliasRowTrigger(UNATTACHED_ERUN_ALIAS)).toBeVisible();

    await app.sidebar.openCloudAliasPopover(UNATTACHED_ERUN_ALIAS);
    await expect(app.sidebar.cloudAliasPopoverButton(/Log out|Logging out/)).toBeVisible();
  });
});

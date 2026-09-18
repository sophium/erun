import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// sidebarCloudAliases resolved a tenant's own cloudProviderAliases and
// returned nothing at all when no tenant referenced the alias -- the
// ordinary shape for a hosted erun platform reached from a machine whose
// tenants are all local. The alias then never appeared in the sidebar no
// matter which tenant was selected.
//
// The seeded baseline's own aliases (SEED_CLOUD_ALIAS, SEED_CLOUDFLARE_ALIAS)
// are both attached to the pw tenant, so this stubs LoadState with a minimal
// boot payload instead of extending the shared seed -- extending the shared
// seed with a third, unattached alias would also change
// cloudflare-cloud-alias.spec.ts's per-tenant row-count assertion for every
// other spec in the suite. Mirrors sidebar-env-activity-boot-snapshot.spec.ts's
// stub-LoadState-then-reboot pattern.

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

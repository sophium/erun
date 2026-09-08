import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// See tenant-dashboard-audit-log.spec.ts for why this stages a throwaway env
// with an apiUrl and stubs the RPC.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

async function stubLoadTenantDashboard(
  page: import('@playwright/test').Page,
  data: Record<string, unknown>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadTenantDashboard') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
      return;
    }
    await route.continue();
  });
}

// erun#2050: the Users tab shows the signed-in operator's own erun username
// (whoami's own `username`, the same one the console's identity-header and
// audit trail use) and, now, the OIDC subject too -- the one value that
// reliably joins this row against the same person's entry in the console's
// separate IdP-identity Users list, which has no reachable erun username of
// its own to compare against. Neither surface previously showed the subject
// at all.
test.describe('tenant dashboard — users tab (#2050)', () => {
  test('renders the signed-in operator with roles and their OIDC subject', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('users-tab-subject');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: {
          tenantId: 't1',
          userId: 'u1',
          username: 'erun',
          roles: ['ReadAll', 'WriteAll'],
          issuer: 'https://auth.example.com',
          subject: '386994597031248060',
        },
        panels: [{ tab: 'users' }],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Users');

      await expect(app.tenantDashboard.usersRows()).toHaveCount(1);
      const row = app.tenantDashboard.usersRows().first();
      await expect(row).toContainText('erun');
      await expect(row).toContainText('ReadAll');
      await expect(row).toContainText('WriteAll');
      await expect(row).toContainText('386994597031248060');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

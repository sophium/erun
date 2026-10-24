import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

interface StubbedResponse {
  data?: Record<string, unknown>;
  error?: string;
}

// Mutable holder so a test can change the answer before Refresh and count calls.
function stubLoadTenantDashboard(
  page: import('@playwright/test').Page,
  initial: StubbedResponse,
): { calls: () => number; respondWith: (next: StubbedResponse) => void } {
  let current = initial;
  let calls = 0;
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadTenantDashboard') {
      calls += 1;
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(current.error ? { error: current.error } : { data: current.data }),
      });
      return;
    }
    await route.continue();
  });
  return {
    calls: () => calls,
    respondWith: (next) => {
      current = next;
    },
  };
}

test.describe('tenant dashboard — refresh (#1213)', () => {
  test('clicking Refresh re-fetches instead of replaying the cached result', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('refresh-refetches');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      const stub = stubLoadTenantDashboard(page, {
        data: {
          tenant: SEED_TENANT,
          environment,
          apiUrl: 'http://127.0.0.1:1/unreachable',
          user: { tenantId: 't1', userId: 'u1', username: 'operator' },
          builds: [{ buildId: 'build-1', successful: true, commitId: 'c1', version: '1.0.0' }],
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');
      await expect(app.tenantDashboard.activePanel()).toContainText('build-1');
      expect(stub.calls()).toBe(1);

      // A cached replay would keep showing build-1.
      stub.respondWith({
        data: {
          tenant: SEED_TENANT,
          environment,
          apiUrl: 'http://127.0.0.1:1/unreachable',
          user: { tenantId: 't1', userId: 'u1', username: 'operator' },
          builds: [{ buildId: 'build-2', successful: true, commitId: 'c2', version: '1.0.1' }],
        },
      });

      await app.tenantDashboard.clickRefresh();

      await expect(app.tenantDashboard.activePanel()).toContainText('build-2');
      await expect(app.tenantDashboard.activePanel()).not.toContainText('build-1');
      expect(stub.calls()).toBe(2);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a failing refresh reports the failure, not a success toast', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('refresh-reports-failure');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      const stub = stubLoadTenantDashboard(page, {
        data: {
          tenant: SEED_TENANT,
          environment,
          apiUrl: 'http://127.0.0.1:1/unreachable',
          user: { tenantId: 't1', userId: 'u1', username: 'operator' },
          builds: [{ buildId: 'build-1', successful: true, commitId: 'c1', version: '1.0.0' }],
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      stub.respondWith({ error: 'backend unreachable' });
      await app.tenantDashboard.clickRefresh();

      // Wait for the error before asserting no toast, rather than racing auto-dismiss.
      await expect(page.getByText('backend unreachable')).toBeVisible();

      await expect(app.titlebar.messageCenterIcon('success')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

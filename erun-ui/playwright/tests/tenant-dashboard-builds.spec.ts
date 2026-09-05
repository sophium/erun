import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// erun#1954: a build no longer has to belong to a review -- an ordinary
// `erun build` self-reports itself against the environment it ran in
// instead. This exercises the Builds tab's rendering of that unattached
// shape over a stubbed RPC, the same pattern tenant-dashboard-refresh.spec.ts
// uses for the review-linked shape; the Go side that merges GET /v1/builds
// into the panel is covered by erun-ui/tenant_dashboard_test.go.
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

function dashboardData(
  environment: string,
  extra: Record<string, unknown>,
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    ...extra,
  };
}

test.describe('tenant dashboard — builds with no review (#1954)', () => {
  test('an unattached build renders its environment as the Source, not a review', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('builds-unattached');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          builds: [
            {
              buildId: 'build-unattached-1',
              environmentId: 'env-1',
              environmentName: 'ci-runner',
              successful: true,
              commitId: 'abc123',
              version: '1.0.0-snapshot-20260101010101',
            },
            {
              buildId: 'build-review-1',
              reviewId: 'review-1',
              reviewName: 'Add widget',
              successful: false,
              commitId: 'def456',
              version: '1.2.3',
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');

      const panel = app.tenantDashboard.activePanel();
      await expect(panel).toContainText('build-unattached-1');
      await expect(panel).toContainText('env: ci-runner');
      await expect(panel).toContainText('build-review-1');
      await expect(panel).toContainText('Add widget');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the empty state names both a review and an environment as the source', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('builds-empty-state');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, dashboardData(environment, { builds: [] }));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');

      await expect(app.tenantDashboard.activePanel().getByText('No builds yet')).toBeVisible();
      await expect(app.tenantDashboard.activePanel()).toContainText('erun build');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

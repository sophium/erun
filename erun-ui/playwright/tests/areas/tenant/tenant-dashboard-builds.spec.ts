import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

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

// erun#2274: "operator should be able to go to builds, select builds and see
// exactly what consumes cpu or if there has been IO bottlenecks". These cover
// the per-build profile dialog opened from each build row's own "View
// profile" button.
test.describe('tenant dashboard — build profile (#2274)', () => {
  test('a build with a full profile shows per-step duration, CPU, throttling, I/O, and peak memory', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('build-profile-full');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          builds: [
            {
              buildId: 'build-profiled-1',
              environmentId: 'env-1',
              environmentName: 'ci-runner',
              successful: true,
              commitId: 'abc123',
              version: '1.0.0-snapshot-20260101010101',
              profile: {
                durationSeconds: 199.8,
                totalStepCount: 2,
                topSteps: [
                  {
                    name: 'erun-devops',
                    durationSeconds: 198.9,
                    cgroup: {
                      available: true,
                      cpuSeconds: 11.13,
                      cpuPercentOfQuota: 17.6,
                      throttledPeriods: 7,
                      totalPeriods: 102,
                      throttledSeconds: 1.14,
                      ioReadBytes: 921452544,
                      ioWriteBytes: 70008832,
                      peakMemoryBytes: 9489924096,
                    },
                  },
                  {
                    name: 'erun-devops > linux/amd64',
                    durationSeconds: 198.6,
                    cgroup: { available: true, cpuSeconds: 11.0, cpuPercentOfQuota: 17.5 },
                  },
                ],
              },
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');
      await app.tenantDashboard.buildProfileButtonFor('build-profiled-1').click();

      await app.buildProfileDialog.waitForOpen();
      const dialog = app.buildProfileDialog.locator();
      await expect(dialog).toContainText('abc123');
      await expect(dialog).toContainText('erun-devops');
      await expect(dialog).toContainText('throttled 7/102 periods');
      await expect(dialog).toContainText('18% of quota');
      await expect(dialog).toContainText('878.8 MiB read');
      await expect(dialog).toContainText('66.8 MiB written');
      await expect(dialog).toContainText('8.84 GiB');
      await expect(app.buildProfileDialog.stepRows()).toHaveCount(2);
      await expect(app.buildProfileDialog.notAvailableNotice()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a build with no profile renders a plain empty state, not zeros', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('build-profile-none');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          builds: [
            {
              buildId: 'build-no-profile-1',
              environmentId: 'env-1',
              environmentName: 'ci-runner',
              successful: true,
              commitId: 'def456',
              version: '1.0.0',
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');
      await app.tenantDashboard.buildProfileButtonFor('build-no-profile-1').click();

      await app.buildProfileDialog.waitForOpen();
      await expect(app.buildProfileDialog.noProfileEmptyState()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a profile with no cgroup data says so plainly instead of implying zero usage', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('build-profile-unavailable');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          builds: [
            {
              buildId: 'build-laptop-1',
              environmentId: 'env-1',
              environmentName: 'ci-runner',
              successful: true,
              commitId: 'ghi789',
              version: '1.0.0',
              profile: {
                durationSeconds: 42,
                totalStepCount: 1,
                topSteps: [{ name: 'erun-devops', durationSeconds: 40 }],
              },
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');
      await app.tenantDashboard.buildProfileButtonFor('build-laptop-1').click();

      await app.buildProfileDialog.waitForOpen();
      await expect(app.buildProfileDialog.notAvailableNotice()).toBeVisible();
      // Every cgroup-derived cell (CPU, throttling, I/O, peak memory) reads
      // "Not available" for a step with no cgroup reading -- never a zero,
      // which would misread as "this step used no CPU".
      await expect(app.buildProfileDialog.stepRows().getByText('Not available')).toHaveCount(4);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a truncated profile names how many steps were left out', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('build-profile-truncated');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      const topSteps = Array.from({ length: 10 }, (_, i) => ({
        name: `step-${String(i)}`,
        durationSeconds: 10 - i,
      }));
      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          builds: [
            {
              buildId: 'build-truncated-1',
              environmentId: 'env-1',
              environmentName: 'ci-runner',
              successful: true,
              commitId: 'jkl012',
              version: '1.0.0',
              profile: {
                durationSeconds: 100,
                totalStepCount: 15,
                truncatedStepCount: 5,
                topSteps,
              },
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Builds');
      await app.tenantDashboard.buildProfileButtonFor('build-truncated-1').click();

      await app.buildProfileDialog.waitForOpen();
      await expect(app.buildProfileDialog.truncatedNote()).toContainText('5 more steps');
      await expect(app.buildProfileDialog.stepRows()).toHaveCount(10);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

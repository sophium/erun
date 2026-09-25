import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Mirrors tenant-dashboard-gates.spec.ts's own setup: the dashboard only calls
// LoadTenantDashboard once it can resolve an API URL, so this stages one
// throwaway env carrying one, purely to satisfy that gate -- the RPC itself is
// stubbed below rather than hitting a real backend.
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

// A unit of planned work exists only as a record: no branch, no commit, no
// review. It is what neither the Reviews tab nor the merge queue can show.
const plannedJob = {
  jobId: 'job-planned',
  jobType: 'triage',
  issueRef: 'sophium/erun#2683',
  summary: 'plan the pipeline view',
  status: 'PLANNED',
  actorKind: 'orchestrator',
  actorId: 'erun/ideas',
  startedAt: '2026-09-25T09:00:00Z',
};

test.describe('tenant dashboard — pipeline tab', () => {
  // The whole point of the view: work, and the review beside it, filed under
  // the one issue they belong to -- labelled by the rung each stands on. A
  // planned job and a review are different records, and the row says which.
  test('unions planned work with the review pipeline, labelled by rung', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('pipeline-union');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          panels: [{ tab: 'pipeline' }],
          pipeline: [
            {
              issueKey: 'sophium/erun#2683',
              items: [
                {
                  issueKey: 'sophium/erun#2683',
                  issueRef: 'sophium/erun#2683',
                  issueRefSource: 'DECLARED',
                  rung: 'PLANNED',
                  job: plannedJob,
                },
                {
                  issueKey: 'sophium/erun#2683',
                  issueRef: '2683',
                  issueRefSource: 'DECLARED',
                  rung: 'REVIEW_OPEN',
                  review: {
                    reviewId: 'review-1',
                    repository: 'github.com/sophium/erun',
                    name: 'Add the view',
                    targetBranch: 'main',
                    sourceBranch: 'feature/2683-pipeline-view',
                    status: 'OPEN',
                  },
                },
              ],
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      await expect(app.tenantDashboard.pipelineRows()).toHaveCount(2);
      const plannedRow = app.tenantDashboard.pipelineRows().first();
      await expect(plannedRow).toContainText('Planned');
      // The job states what kind of work it is and who holds it, and claims
      // no branch.
      await expect(plannedRow).toContainText('plan the pipeline view');
      await expect(plannedRow).toContainText('job · triage · erun/ideas');
      await expect(plannedRow).toContainText('sophium/erun#2683');

      const openRow = app.tenantDashboard.pipelineRows().last();
      await expect(openRow).toContainText('Review open');
      await expect(openRow).toContainText('Add the view');
      await expect(openRow).toContainText('feature/2683-pipeline-view');
      // Both halves are filed under the same issue: that is the union.
      await expect(openRow).toContainText('sophium/erun#2683');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The provenance rule, rendered: a number parsed out of a branch name is a
  // guess about the branch and must not read as something the author declared.
  test('marks a branch-derived issue link as inferred', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('pipeline-inferred');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          panels: [{ tab: 'pipeline' }],
          pipeline: [
            {
              issueKey: 'sophium/erun#2212',
              items: [
                {
                  issueKey: 'sophium/erun#2212',
                  issueRef: '2212',
                  issueRefSource: 'INFERRED',
                  rung: 'REVIEW_OPEN',
                  review: {
                    reviewId: 'review-2',
                    name: 'Add widget',
                    targetBranch: 'main',
                    sourceBranch: 'bug/2212-issue-ref-from-branch',
                    status: 'OPEN',
                  },
                },
              ],
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      const row = app.tenantDashboard.pipelineRows().first();
      await expect(row).toContainText('sophium/erun#2212');
      await expect(row).toContainText('inferred from branch');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The unlinked group is why the view shows work without an issue rather than
  // guessing one for it. An empty cell would read as a row that failed to
  // load; the panel names the absence instead.
  test('shows work that names no issue without inventing one', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('pipeline-unlinked');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          panels: [{ tab: 'pipeline' }],
          pipeline: [
            {
              issueKey: '',
              items: [
                {
                  issueKey: '',
                  rung: 'FAILED',
                  job: { ...plannedJob, jobId: 'job-2', summary: 'a fix that did not land' },
                },
              ],
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      const row = app.tenantDashboard.pipelineRows().first();
      await expect(row).toContainText('No issue');
      await expect(row).toContainText('Failed');
      await expect(row).toContainText('a fix that did not land');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A rung this build has never heard of is the platform being ahead of the
  // desktop. It renders under its own spelling rather than being folded into a
  // rung it resembles, which would describe work the panel cannot see.
  test('renders an unrecognised rung under its own spelling', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('pipeline-unknown-rung');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          panels: [{ tab: 'pipeline' }],
          pipeline: [
            {
              issueKey: 'sophium/erun#1',
              items: [
                {
                  issueKey: 'sophium/erun#1',
                  rung: 'QUARANTINED',
                  job: { ...plannedJob, jobId: 'job-3', summary: 'work in an unknown state' },
                },
              ],
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      const row = app.tenantDashboard.pipelineRows().first();
      await expect(row).toContainText('QUARANTINED');
      await expect(row).toContainText('work in an unknown state');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('shows a purpose-built empty state when the tenant has nothing in the pipeline', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('pipeline-empty');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, { panels: [{ tab: 'pipeline' }], pipeline: [] }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      await expect(app.tenantDashboard.pipelineEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.pipelineTable()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The state a caller actually hits while the platform is older than this
  // build: GET /v1/pipeline 404s, so the tab is a failed read, not a tenant
  // with nothing going on. A regression that let it fall through to the empty
  // state would read as "nothing is happening" while the view was simply
  // unreadable -- and the neighbouring panel answered, so it must still show
  // what it answered.
  test('reports a failed pipeline read as a failure, not as an empty pipeline', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('pipeline-read-failure');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          pipeline: [],
          panels: [
            {
              tab: 'pipeline',
              error: 'load tenant dashboard GET /v1/pipeline: http 404: 404 page not found',
            },
            { tab: 'audit' },
          ],
          auditEvents: [
            {
              type: 'CLI',
              actor: 'subject-2',
              action: 'erun job start',
              createdAt: '2026-01-02T00:00:00Z',
            },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Pipeline');

      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toContainText(
        'GET /v1/pipeline: http 404',
      );
      await expect(app.tenantDashboard.pipelineEmptyState()).toHaveCount(0);
      await expect(app.tenantDashboard.pipelineTable()).toHaveCount(0);

      await app.tenantDashboard.selectTab('Audit log');
      await expect(app.tenantDashboard.auditRows()).toHaveCount(1);
      await expect(app.tenantDashboard.auditTable()).toContainText('erun job start');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

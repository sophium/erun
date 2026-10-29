import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The dashboard loads only once an env has an apiUrl, so stage one; the RPC
// itself is stubbed.
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

// Planned work is only a record: no branch, commit, or review.
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
      await expect(plannedRow).toContainText('plan the pipeline view');
      await expect(plannedRow).toContainText('job · triage · erun/ideas');
      await expect(plannedRow).toContainText('sophium/erun#2683');

      const openRow = app.tenantDashboard.pipelineRows().last();
      await expect(openRow).toContainText('Review open');
      await expect(openRow).toContainText('Add the view');
      await expect(openRow).toContainText('feature/2683-pipeline-view');
      await expect(openRow).toContainText('sophium/erun#2683');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A number parsed from a branch name is a guess, not something the author declared.
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

  // An unknown rung renders under its own spelling rather than a similar known one.
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

  // An older platform 404s GET /v1/pipeline; that is a failed read, not an empty
  // pipeline, and the other panel must still render.
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

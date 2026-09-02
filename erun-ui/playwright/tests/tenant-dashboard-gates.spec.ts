import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// Mirrors tenant-dashboard-audit-log.spec.ts's own setup comment: the
// dashboard only calls LoadTenantDashboard once it can resolve an API URL,
// so this stages one throwaway env carrying one, purely to satisfy that
// gate -- the RPC itself is stubbed below rather than hitting a real
// backend.
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

test.describe('tenant dashboard — gates tab (erun#1932)', () => {
  test('renders returned gate runs with branch, verdict, and a failed step + log ref', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('gates-populated');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        gateRuns: [
          {
            gateRunId: 'gate-1',
            sourceBranch: 'feature/1932-gate-run-ui',
            targetBranch: 'main',
            sourceCommit: 'abcdef123456',
            mergeCommit: 'fedcba654321',
            status: 'RUNNING',
            createdAt: '2026-09-02T10:00:00Z',
            updatedAt: '2026-09-02T10:00:00Z',
          },
          {
            gateRunId: 'gate-2',
            sourceBranch: 'feature/other',
            targetBranch: 'main',
            sourceCommit: 'aaa111',
            mergeCommit: 'bbb222',
            status: 'FAILED',
            failingStep: 'build',
            logRef: 'job-42',
            createdAt: '2026-09-02T09:00:00Z',
            updatedAt: '2026-09-02T09:05:00Z',
          },
        ],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Gates');

      await expect(app.tenantDashboard.gatesRows()).toHaveCount(2);
      const runningRow = app.tenantDashboard.gatesRows().first();
      await expect(runningRow).toContainText('RUNNING');
      await expect(runningRow).toContainText('feature/1932-gate-run-ui');
      const failedRow = app.tenantDashboard.gatesRows().last();
      await expect(failedRow).toContainText('FAILED');
      await expect(failedRow).toContainText('build');
      await expect(failedRow).toContainText('job-42');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The one thing to get right above all (erun#1932): INCONCLUSIVE must not
  // render as a failure. It exists precisely because a wrapper hitting its
  // own timeout, or an environment fault, is not a verdict on the change --
  // so it must read as its own distinct state, never folded into FAILED's.
  test('renders INCONCLUSIVE as its own distinct state, not as a failure', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('gates-inconclusive');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        gateRuns: [
          {
            gateRunId: 'gate-3',
            sourceBranch: 'feature/timeout',
            targetBranch: 'main',
            sourceCommit: 'ccc333',
            status: 'INCONCLUSIVE',
            createdAt: '2026-09-02T08:00:00Z',
            updatedAt: '2026-09-02T08:10:00Z',
          },
        ],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Gates');

      const row = app.tenantDashboard.gatesRows().first();
      await expect(row).toContainText('INCONCLUSIVE');
      await expect(row).not.toContainText('FAILED');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('shows a purpose-built empty state, not an input-styled box, when there are no gate runs', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('gates-empty');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        gateRuns: [],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Gates');

      await expect(app.tenantDashboard.gatesEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.gatesTable()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

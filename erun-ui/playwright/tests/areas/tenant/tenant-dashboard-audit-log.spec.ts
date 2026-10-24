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

test.describe('tenant dashboard — audit log tab (#1205)', () => {
  test('renders returned audit events with time, type, actor, and action', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('audit-log-populated');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        auditEvents: [
          {
            type: 'API',
            actor: 'subject-1',
            action: 'GET /v1/reviews',
            createdAt: '2026-01-01T00:00:00Z',
          },
          {
            type: 'CLI',
            actor: 'subject-2',
            action: 'erun build',
            createdAt: '2026-01-02T00:00:00Z',
          },
          {
            type: 'MCP',
            actor: 'subject-3',
            action: 'cloud_inject_aws_credentials',
            createdAt: '2026-01-03T00:00:00Z',
          },
        ],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Audit log');

      await expect(app.tenantDashboard.auditRows()).toHaveCount(3);
      const firstRow = app.tenantDashboard.auditRows().first();
      await expect(firstRow).toContainText('API');
      await expect(firstRow).toContainText('subject-1');
      await expect(firstRow).toContainText('GET /v1/reviews');
      // Tool names render, but never argument payloads, which can carry credentials.
      await expect(app.tenantDashboard.auditTable()).toContainText('cloud_inject_aws_credentials');
      await expect(app.tenantDashboard.auditTable()).not.toContainText('secret');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('shows a purpose-built empty state, not an input-styled box, when there are no events', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('audit-log-empty');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        auditEvents: [],
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Audit log');

      await expect(app.tenantDashboard.auditEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.auditTable()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

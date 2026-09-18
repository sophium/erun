import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  removeEnvironment,
  seedEnvironment,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// tenant-dashboard-requests.spec.ts covers the operator/admin queue (the
// Requests tab, TenantDashboardPanels.Requests.tsx): who is waiting, and the
// Issue invitation / Decline actions -- the desktop half of #1682 alongside
// request-invitation-dialog.spec.ts's requester-side dialog. Like
// tenant-dashboard-registration.spec.ts, this stubs the __erun_invoke RPC
// boundary rather than a real erun-backend-api, which the inert harness
// deliberately cannot reach; the Go side is covered by
// erun-ui/tenant_platform_invite_requests_test.go, and the console's own
// equivalent (including the capability-gated case) is covered by
// erun-console/src/requests/RequestsPanel.test.tsx.

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

// Applies the app's own class-based light/dark mechanism directly (root
// AGENTS.md's Design-Language Decision Record: one shared `.dark` class
// mechanism), the same escape hatch manage-dialog-status-badge.spec.ts uses,
// rather than clicking the titlebar toggle -- Radix's Dialog primitive marks
// the rest of the app aria-hidden while the decline dialog is open, so the
// titlebar button is unreachable via an accessible role query until it closes.
async function forceDarkTheme(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
}

interface RouteResult {
  data?: unknown;
  error?: string;
}

async function routeInvoke(
  page: import('@playwright/test').Page,
  handlers: Record<string, (request: Request) => RouteResult>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    const handler = handlers[body.method];
    if (handler) {
      const result = handler(request);
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(result.error ? { error: result.error } : { data: result.data }),
      });
      return;
    }
    await route.continue();
  });
}

interface RequestsFixture {
  canApprove?: boolean;
  canDecline?: boolean;
  inviteRequests?: Record<string, unknown>[];
}

function requestsDashboardData(
  environment: string,
  fixture: RequestsFixture,
): Record<string, unknown> {
  const requests = fixture.inviteRequests ?? [];
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator', roles: ['Admin'] },
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canApproveInviteRequests: fixture.canApprove ?? true,
    canDeclineInviteRequests: fixture.canDecline ?? true,
    inviteRequests: requests,
    pendingInviteRequestCount: requests.length,
  };
}

const PENDING_REQUEST = {
  inviteRequestId: 'ir-1',
  issuer: 'https://idp.example.com',
  subject: 'newcomer-1',
  kind: 'JOIN_TENANT',
  tenantName: SEED_TENANT,
  note: 'Already running things locally, just need a seat.',
  status: 'PENDING',
  createdAt: '2026-06-24T10:00:00Z',
  updatedAt: '2026-06-24T10:00:00Z',
};

test.describe('tenant dashboard — Requests tab', () => {
  test('renders the empty state when there are no pending requests, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('requests-empty');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({ data: requestsDashboardData(environment, {}) }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Requests');

      await expect(app.tenantDashboard.requestsEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.requestsTable()).toHaveCount(0);

      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-empty-light.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
      await expect(app.tenantDashboard.requestsEmptyState()).toBeVisible();
      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-empty-dark.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('lists a pending request with its requester, kind, note, and actions, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('requests-populated');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: requestsDashboardData(environment, { inviteRequests: [PENDING_REQUEST] }),
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Requests');

      const row = app.tenantDashboard.requestRowFor('newcomer-1');
      await expect(row).toContainText('Join pw');
      await expect(row).toContainText('Already running things locally');
      await expect(row).toContainText('PENDING');
      await expect(app.tenantDashboard.issueInvitationButtonFor('newcomer-1')).toBeVisible();
      await expect(app.tenantDashboard.declineButtonFor('newcomer-1')).toBeVisible();

      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-populated-light.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
      await expect(row).toBeVisible();
      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-populated-dark.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('issuing an invitation shows the minted link and the row disappears once the list refetches, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('requests-issue');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      let approved = false;
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: requestsDashboardData(environment, {
            inviteRequests: approved ? [] : [PENDING_REQUEST],
          }),
        }),
        ApproveTenantInviteRequest: () => {
          approved = true;
          return {
            data: { ...PENDING_REQUEST, status: 'APPROVED', mintedInviteToken: 'tok-abc' },
          };
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Requests');

      await app.tenantDashboard.issueInvitationButtonFor('newcomer-1').click();

      const notice = app.tenantDashboard.issuedInviteLinkNotice();
      await expect(notice).toBeVisible();
      await expect(notice).toContainText('tok-abc');
      await expect(app.tenantDashboard.requestsEmptyState()).toBeVisible();

      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-issued-light.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
      await expect(notice).toBeVisible();
      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-issued-dark.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('declining a request requires a non-empty reason, sends it, and re-fetches the queue, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('requests-decline');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      let declineBody: Record<string, unknown> | undefined;
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: requestsDashboardData(environment, {
            inviteRequests: declineBody ? [] : [PENDING_REQUEST],
          }),
        }),
        DeclineTenantInviteRequest: (request) => {
          const body = JSON.parse(request.postData() ?? '{}') as {
            args: [Record<string, unknown>];
          };
          declineBody = body.args[0];
          return {
            data: { ...PENDING_REQUEST, status: 'DECLINED', declineReason: declineBody.reason },
          };
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Requests');

      await app.tenantDashboard.declineButtonFor('newcomer-1').click();
      const dialog = app.tenantDashboard.declineRequestDialog();
      await expect(dialog).toBeVisible();
      const confirm = app.tenantDashboard.declineConfirmButton();
      await expect(confirm).toBeDisabled();

      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-decline-dialog-light.png',
        animations: 'disabled',
      });
      await forceDarkTheme(page);
      await expect(dialog).toBeVisible();
      await page.screenshot({
        path: 'test-results/tenant-dashboard-requests-decline-dialog-dark.png',
        animations: 'disabled',
      });

      await app.tenantDashboard.declineReasonInput().fill('no room right now');
      await expect(confirm).toBeEnabled();
      await confirm.click();

      await expect.poll(() => declineBody?.reason).toBe('no room right now');
      await expect(app.tenantDashboard.requestsEmptyState()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The desktop's own equivalent of the console's capability-gated case
  // (RequestsPanel.test.tsx): the queue itself stays visible (partial
  // degradation), but the actions the caller may not use do not render, and
  // the missing access is still named (root AGENTS.md "Degrade by
  // permission").
  test('names the missing access when the caller can neither issue nor decline', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('requests-restricted-actions');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: requestsDashboardData(environment, {
            canApprove: false,
            canDecline: false,
            inviteRequests: [PENDING_REQUEST],
          }),
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Requests');

      await expect(app.tenantDashboard.requestRowFor('newcomer-1')).toBeVisible();
      await expect(app.tenantDashboard.requestsPermissionNotice()).toBeVisible();
      await expect(app.tenantDashboard.issueInvitationButtonFor('newcomer-1')).toHaveCount(0);
      await expect(app.tenantDashboard.declineButtonFor('newcomer-1')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

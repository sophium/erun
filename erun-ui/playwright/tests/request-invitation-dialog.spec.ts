import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  seedEnvironment,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// request-invitation-dialog.spec.ts covers "Request an invitation" — the
// RequestInvitationDialog opened from NotEnrolledState (#1682's other new
// desktop surface, alongside tenant-dashboard-requests.spec.ts's operator
// queue). Like tenant-dashboard-platform-state.spec.ts, this stubs the
// __erun_invoke RPC boundary rather than a real erun-backend-api, which the
// inert harness deliberately cannot reach; the Go side is covered by
// erun-ui/tenant_platform_invite_requests_test.go.

// Applies the app's own class-based light/dark mechanism directly (root
// AGENTS.md's Design-Language Decision Record: one shared `.dark` class
// mechanism), the same escape hatch manage-dialog-status-badge.spec.ts uses,
// rather than clicking the titlebar toggle -- Radix's Dialog primitive marks
// the rest of the app aria-hidden while a modal is open, so the titlebar
// button is unreachable via an accessible role query for as long as this
// dialog stays open.
async function forceDarkTheme(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
}

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
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

function notEnrolledData(myInviteRequest?: Record<string, unknown>): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    platformState: 'not-enrolled',
    platformAlias: 'erun+api.frs-prod.services.erunpaas.com@erun',
    platformIssuer: 'https://auth.erunpaas.com',
    platformSubject: 'user-42',
    myInviteRequest,
  };
}

test.describe('request an invitation dialog', () => {
  test('opens with the verified identity and the join/register kind tabs, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('request-invite-open');
    try {
      await routeInvoke(page, { LoadTenantDashboard: () => ({ data: notEnrolledData() }) });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notEnrolledHeading()).toBeVisible();
      await app.tenantDashboard.requestInvitationButton().click();

      const dialog = app.tenantDashboard.requestInvitationDialog();
      await expect(dialog).toBeVisible();
      await expect(app.tenantDashboard.requestInvitationIdentitySummary()).toBeVisible();
      await expect(dialog).toContainText('user-42');
      await expect(dialog).toContainText('https://auth.erunpaas.com');
      await expect(app.tenantDashboard.requestKindJoinTab()).toHaveAttribute(
        'aria-selected',
        'true',
      );
      await expect(app.tenantDashboard.requestKindCreateTab()).toBeVisible();
      await expect(app.tenantDashboard.requestNoteInput()).toBeVisible();
      await expect(app.tenantDashboard.requestSubmitButton()).toBeEnabled();

      await page.screenshot({
        path: 'test-results/request-invitation-dialog-open-light.png',
        animations: 'disabled',
      });
      await forceDarkTheme(page);
      await expect(dialog).toBeVisible();
      await page.screenshot({
        path: 'test-results/request-invitation-dialog-open-dark.png',
        animations: 'disabled',
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The dialog's own guard against a too-soon resubmit is the closest thing
  // this form has to field validation (there is nothing else to validate —
  // identity is never typed in, kind always has a default). It must read as
  // recoverable (root AGENTS.md's "blocked, not broken"), not as an error.
  test('a rate-limited submit shows the countdown as a recoverable status, not an error, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('request-invite-rate-limited');
    try {
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({ data: notEnrolledData() }),
        SubmitTenantInviteRequest: () => ({ data: { rateLimited: { retryAfterSeconds: 5 } } }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.requestInvitationButton().click();
      await app.tenantDashboard.requestSubmitButton().click();

      const reason = app.tenantDashboard.requestSubmitDisabledReason();
      await expect(reason).toContainText('You can send another request in');
      await expect(reason).toHaveAttribute('role', 'status');
      await expect(app.tenantDashboard.requestSubmitButton()).toBeDisabled();
      // Never rendered as a fault: no alert carries this outcome.
      await expect(app.tenantDashboard.requestInvitationDialog().getByRole('alert')).toHaveCount(0);
      // The dialog stays open -- a rate limit is not a reason to lose the draft.
      await expect(app.tenantDashboard.requestInvitationDialog()).toBeVisible();

      await page.screenshot({
        path: 'test-results/request-invitation-dialog-rate-limited-light.png',
        animations: 'disabled',
      });
      await forceDarkTheme(page);
      await expect(reason).toBeVisible();
      await page.screenshot({
        path: 'test-results/request-invitation-dialog-rate-limited-dark.png',
        animations: 'disabled',
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a successful submit closes the dialog and shows the pending status, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('request-invite-success');
    try {
      let submitted = false;
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: notEnrolledData(
            submitted
              ? {
                  inviteRequestId: 'req-1',
                  status: 'PENDING',
                  kind: 'JOIN_TENANT',
                  tenantName: SEED_TENANT,
                }
              : undefined,
          ),
        }),
        SubmitTenantInviteRequest: () => {
          submitted = true;
          return {
            data: {
              request: {
                inviteRequestId: 'req-1',
                status: 'PENDING',
                kind: 'JOIN_TENANT',
                tenantName: SEED_TENANT,
              },
            },
          };
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.requestInvitationButton().click();
      await app.tenantDashboard.requestSubmitButton().click();

      await expect(app.tenantDashboard.requestInvitationDialog()).toHaveCount(0);
      await expect(app.tenantDashboard.requestPendingStatus()).toBeVisible();

      await page.screenshot({
        path: 'test-results/request-invitation-dialog-submitted-pending-light.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
      await expect(app.tenantDashboard.requestPendingStatus()).toBeVisible();
      await page.screenshot({
        path: 'test-results/request-invitation-dialog-submitted-pending-dark.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A submit failure must be visible, not a silent no-op: the dialog stays
  // open and an InlineAlert names what happened (root AGENTS.md's "an action
  // that succeeds and changes nothing on screen" dead-end applies just as
  // much to an action that fails and shows nothing).
  test('a failed submit renders a visible error and keeps the dialog open, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('request-invite-failed');
    try {
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({ data: notEnrolledData() }),
        SubmitTenantInviteRequest: () => ({
          error: 'platform api: internal server error',
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.requestInvitationButton().click();
      await app.tenantDashboard.requestSubmitButton().click();

      const alert = app.tenantDashboard.requestErrorAlert();
      await expect(alert).toBeVisible();
      await expect(alert).toContainText('internal server error');
      await expect(app.tenantDashboard.requestInvitationDialog()).toBeVisible();
      await expect(app.tenantDashboard.requestSubmitButton()).toBeEnabled();

      await page.screenshot({
        path: 'test-results/request-invitation-dialog-failed-light.png',
        animations: 'disabled',
      });
      await forceDarkTheme(page);
      await expect(alert).toBeVisible();
      await page.screenshot({
        path: 'test-results/request-invitation-dialog-failed-dark.png',
        animations: 'disabled',
      });

      // Cancel still backs out cleanly after a failure.
      await app.tenantDashboard.requestCancelButton().click();
      await expect(app.tenantDashboard.requestInvitationDialog()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // Guards tenant_platform_invite_requests.go's loadTenantDashboardMyInviteRequest:
  // a transport failure reading the caller's own invite request must never
  // read as "never requested" (which would offer "Request an invitation"
  // again to someone who may already have a pending or approved request).
  test('a failed invite-request status read shows a visible error, not a fresh "Request an invitation" offer, both themes', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('request-invite-status-check-failed');
    try {
      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          data: {
            ...notEnrolledData(),
            myInviteRequestError: 'platform api GET /v1/invite-requests/mine: http 500',
          },
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notEnrolledHeading()).toBeVisible();

      const alert = app.tenantDashboard.requestStatusCheckFailedAlert();
      await expect(alert).toBeVisible();
      await expect(alert).toContainText('could not be checked');
      await expect(app.tenantDashboard.requestInvitationButton()).toHaveCount(0);
      await expect(app.tenantDashboard.requestStatusCheckRetryButton()).toBeVisible();

      await page.screenshot({
        path: 'test-results/request-invitation-dialog-status-check-failed-light.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
      await expect(alert).toBeVisible();
      await page.screenshot({
        path: 'test-results/request-invitation-dialog-status-check-failed-dark.png',
        animations: 'disabled',
      });
      await app.titlebar.toggleTheme();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

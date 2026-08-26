import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// tenant-dashboard-merge-queue-gate covers the unresolved-thread gate on
// AdvanceMergeQueue: a blocked advance must name the count and the review,
// offer a route to the discussion, and — only for a caller the platform
// grants the distinct override route to — a reason-required override. See
// tenant-dashboard-review-write.spec.ts for the unblocked
// advance/create/close/comment coverage this complements; the Go side of
// both the gate and the override lives in
// erun-backend-api/internal/service/reviews_test.go and
// erun-ui/tenant_review_write_test.go.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

// invokeArgs reads the single-input-struct arg every Wails method here takes,
// the same envelope shape headlessserver.invokeRequest decodes on the wire.
function invokeArgs<T>(request: Request): T {
  const parsed = JSON.parse(request.postData() ?? '{}') as { args?: [T] };
  return (parsed.args?.[0] ?? {}) as T;
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const BLOCKED_REVIEW = {
  reviewId: 'review-blocked',
  tenantId: 't1',
  name: 'Add widget',
  targetBranch: 'main',
  sourceBranch: 'feature/widget',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

function dashboardResponse(
  environment: string,
  overrides: Partial<{
    canAdvanceMergeQueue: boolean;
    canOverrideMergeQueue: boolean;
    mergeQueue: unknown[];
  }>,
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    platformAlias: 'pw-aws',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    reviews: [],
    mergeQueue: overrides.mergeQueue ?? [BLOCKED_REVIEW],
    panels: [
      { tab: 'users' },
      { tab: 'reviews' },
      { tab: 'queue' },
      { tab: 'builds' },
      { tab: 'audit' },
    ],
    canCreateReview: false,
    canAdvanceMergeQueue: overrides.canAdvanceMergeQueue ?? true,
    canOverrideMergeQueue: overrides.canOverrideMergeQueue ?? false,
  };
}

async function openMergeQueueTab(app: AppShell, environment: string): Promise<void> {
  await app.reloadEnvironments();
  await app.sidebar
    .envRowButton(SEED_TENANT, environment)
    .waitFor({ state: 'visible', timeout: 15_000 });
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Merge queue');
}

test.describe('tenant dashboard — merge queue unresolved-thread gate', () => {
  test('a blocked advance names the review and count, and opens its discussion', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('queue-gate-blocked');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, dashboardResponse(environment, {}));
          return;
        }
        if (body.method === 'AdvanceMergeQueue') {
          await fulfillJSON(route, {
            reviewId: BLOCKED_REVIEW.reviewId,
            tenantId: 't1',
            name: '',
            targetBranch: '',
            sourceBranch: '',
            status: '',
            blocked: true,
            unresolvedThreads: 3,
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: BLOCKED_REVIEW.reviewId,
            review: BLOCKED_REVIEW,
            canComment: true,
          });
          return;
        }
        await route.continue();
      });

      await openMergeQueueTab(app, environment);
      await app.tenantDashboard.advanceMergeQueueButton().click();
      await app.tenantDashboard.advanceMergeQueueConfirmButton().click();

      const alert = app.tenantDashboard.mergeQueueBlockedAlert();
      await expect(alert).toBeVisible();
      await expect(alert).toContainText(BLOCKED_REVIEW.name);
      await expect(alert).toContainText('3');
      await expect(alert).toContainText('unresolved');
      // No caller has an unrestricted "always show Override" affordance:
      // canOverrideMergeQueue defaults to false in this scenario.
      await expect(app.tenantDashboard.mergeQueueOverrideAnywayButton()).toHaveCount(0);

      await app.tenantDashboard.mergeQueueViewDiscussionButton().click();
      await app.reviewDetailDialog.waitForOpen();
      await expect(app.reviewDetailDialog.locator()).toContainText(BLOCKED_REVIEW.name);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a caller granted the override route can bypass the gate with a reason', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('queue-gate-override');
    try {
      let overrideBody: { targetBranch?: string; reason?: string } = {};
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, dashboardResponse(environment, { canOverrideMergeQueue: true }));
          return;
        }
        if (body.method === 'AdvanceMergeQueue') {
          await fulfillJSON(route, {
            reviewId: BLOCKED_REVIEW.reviewId,
            tenantId: 't1',
            name: '',
            targetBranch: '',
            sourceBranch: '',
            status: '',
            blocked: true,
            unresolvedThreads: 2,
          });
          return;
        }
        if (body.method === 'OverrideAdvanceMergeQueue') {
          overrideBody = invokeArgs<{ targetBranch?: string; reason?: string }>(request);
          await fulfillJSON(route, { ...BLOCKED_REVIEW, status: 'MERGE' });
          return;
        }
        await route.continue();
      });

      await openMergeQueueTab(app, environment);
      await app.tenantDashboard.advanceMergeQueueButton().click();
      await app.tenantDashboard.advanceMergeQueueConfirmButton().click();
      await expect(app.tenantDashboard.mergeQueueBlockedAlert()).toBeVisible();

      // The override does not act on a single click: it demands a reason.
      await app.tenantDashboard.mergeQueueOverrideAnywayButton().click();
      const reasonInput = app.tenantDashboard.mergeQueueOverrideReasonInput();
      await expect(reasonInput).toBeVisible();
      const confirmButton = app.tenantDashboard.mergeQueueOverrideConfirmButton();
      await expect(confirmButton).toBeDisabled();

      await reasonInput.fill('hotfix, reviewers unavailable');
      await expect(confirmButton).toBeEnabled();
      await confirmButton.click();

      await expect(app.tenantDashboard.mergeQueueBlockedAlert()).toHaveCount(0);
      expect(overrideBody.targetBranch).toBe('main');
      expect(overrideBody.reason).toBe('hotfix, reviewers unavailable');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('cancelling the override leaves the blocked state in place', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('queue-gate-cancel');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, dashboardResponse(environment, { canOverrideMergeQueue: true }));
          return;
        }
        if (body.method === 'AdvanceMergeQueue') {
          await fulfillJSON(route, {
            reviewId: BLOCKED_REVIEW.reviewId,
            tenantId: 't1',
            name: '',
            targetBranch: '',
            sourceBranch: '',
            status: '',
            blocked: true,
            unresolvedThreads: 1,
          });
          return;
        }
        await route.continue();
      });

      await openMergeQueueTab(app, environment);
      await app.tenantDashboard.advanceMergeQueueButton().click();
      await app.tenantDashboard.advanceMergeQueueConfirmButton().click();
      await app.tenantDashboard.mergeQueueOverrideAnywayButton().click();
      await expect(app.tenantDashboard.mergeQueueOverrideReasonInput()).toBeVisible();

      await app.tenantDashboard.mergeQueueOverrideCancelButton().click();

      await expect(app.tenantDashboard.mergeQueueOverrideReasonInput()).toHaveCount(0);
      // Cancelling the override is not cancelling the whole refusal: the
      // operator is still exactly where "resolve the threads instead" holds.
      await expect(app.tenantDashboard.mergeQueueBlockedAlert()).toBeVisible();
      await expect(app.tenantDashboard.mergeQueueViewDiscussionButton()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

import type { Page, Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// The desktop could read a review but do nothing else: no create, no close,
// no merge-queue advance. These specs cover the new write
// surface over the stubbed platform RPC — see tenant-dashboard-reviews.spec.ts
// for why the collaboration API is stubbed rather than staged for real: it
// comes from a hosted erun-backend-api the inert harness deliberately has no
// access to. The Go side is covered by erun-ui/tenant_review_write_test.go.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const REVIEW = {
  reviewId: 'review-1',
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
    canCreateReview: boolean;
    canAdvanceMergeQueue: boolean;
    reviews: unknown[];
    mergeQueue: unknown[];
  }>,
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    reviews: overrides.reviews ?? [],
    mergeQueue: overrides.mergeQueue ?? [],
    panels: [
      { tab: 'users' },
      { tab: 'reviews' },
      { tab: 'queue' },
      { tab: 'builds' },
      { tab: 'audit' },
    ],
    canCreateReview: overrides.canCreateReview ?? false,
    canAdvanceMergeQueue: overrides.canAdvanceMergeQueue ?? false,
  };
}

async function openDashboardReviewsTab(
  app: AppShell,
  environment: string,
  tab: 'Reviews' | 'Merge queue' = 'Reviews',
): Promise<void> {
  await app.reloadEnvironments();
  await app.sidebar
    .envRowButton(SEED_TENANT, environment)
    .waitFor({ state: 'visible', timeout: 15_000 });
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab(tab);
}

test.describe('tenant dashboard — opening a review (#1348)', () => {
  test('pushing the branch then creating the review lands the new review', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-create');
    try {
      let createCalled = false;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(
            route,
            dashboardResponse(environment, {
              canCreateReview: true,
              // Two target branches already in use, so the field has a known
              // set to offer rather than asking the operator to retype one.
              reviews: [REVIEW, { ...REVIEW, reviewId: 'review-2', targetBranch: 'release/1.0' }],
            }),
          );
          return;
        }
        if (body.method === 'EnvironmentWorkingIssue') {
          await fulfillJSON(route, { available: true, branch: 'feature/1348-x' });
          return;
        }
        if (body.method === 'ExecCommit') {
          await fulfillJSON(route, { branch: 'feature/1348-x', commit: 'abc123', files: ['a.go'] });
          return;
        }
        if (body.method === 'ExecPush') {
          await fulfillJSON(route, {
            branch: 'feature/1348-x',
            remote: 'origin',
            commit: 'abc123',
          });
          return;
        }
        if (body.method === 'CreateReview') {
          createCalled = true;
          await fulfillJSON(route, { ...REVIEW, sourceBranch: 'feature/1348-x' });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: { ...REVIEW, sourceBranch: 'feature/1348-x' },
            canComment: true,
          });
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment);
      await expect(app.tenantDashboard.newReviewButton()).toBeVisible();
      await app.tenantDashboard.newReviewButton().click();

      const dialog = app.createReviewDialog;
      await dialog.waitForOpen();
      await expect(dialog.locator()).toContainText('feature/1348-x');
      // Every write in this dialog lands in one environment, so the dialog
      // names it before anything runs.
      await expect(dialog.locator()).toContainText(`${SEED_TENANT} / ${environment}`);
      // Create is unavailable until the review has a name, and says so rather
      // than presenting a dead button.
      await expect(dialog.requirementHint()).toBeVisible();
      await expect(dialog.createButton()).toBeDisabled();

      await dialog.fillCommitMessage('describe the change');
      await dialog.commit();
      await dialog.push();
      await expect(dialog.locator()).toContainText('Pushed to origin/feature/1348-x');

      await dialog.fillName('Add widget');
      await expect(dialog.requirementHint()).toHaveCount(0);

      // The target branch is offered from the branches this tenant's reviews
      // already target. Page-scoped locators from here: the open choices
      // popover is itself a role="dialog".
      await dialog.openTargetBranchChoices();
      await expect(page.getByRole('button', { name: 'release/1.0' })).toBeVisible();
      await page.getByRole('button', { name: 'main', exact: true }).click();
      await expect(dialog.targetBranchInput()).toHaveValue('main');

      await dialog.create();

      await dialog.waitForClosed();
      expect(createCalled).toBe(true);
      await app.reviewDetailDialog.waitForOpen();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a failed commit does not survive the push that follows it', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('review-create-stale-error');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, dashboardResponse(environment, { canCreateReview: true }));
          return;
        }
        if (body.method === 'EnvironmentWorkingIssue') {
          await fulfillJSON(route, { available: true, branch: 'feature/1348-x' });
          return;
        }
        if (body.method === 'ExecCommit') {
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({ error: 'nothing to commit — the working tree is clean' }),
          });
          return;
        }
        if (body.method === 'ExecPush') {
          await fulfillJSON(route, {
            branch: 'feature/1348-x',
            remote: 'origin',
            commit: 'abc123',
          });
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment);
      await app.tenantDashboard.newReviewButton().click();
      const dialog = app.createReviewDialog;
      await dialog.waitForOpen();

      await dialog.fillCommitMessage('describe the change');
      await dialog.commit();
      await expect(dialog.locator().getByRole('alert')).toContainText('nothing to commit');

      await dialog.push();
      await expect(dialog.locator()).toContainText('Pushed to origin/feature/1348-x');
      // The failure belonged to an attempt the operator has moved past; left
      // in place it sits beside the green badge saying the opposite.
      await expect(dialog.locator().getByRole('alert')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('hides the New review action and names the missing access when the caller cannot create one', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-create-restricted');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, dashboardResponse(environment, { canCreateReview: false }));
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment);
      await expect(app.tenantDashboard.newReviewButton()).toHaveCount(0);
      await expect(app.tenantDashboard.reviewsRestrictedNote()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

test.describe('tenant dashboard — closing a review (#1348)', () => {
  async function openReviewWithClosePermission(
    app: AppShell,
    page: Page,
    environment: string,
    canClose: boolean,
  ): Promise<void> {
    // Closing dispatches submitCloseReview, which reloads LoadReviewDetail —
    // the stub must reflect the new status on that reload the same way the
    // real platform would, or the assertion below would pass for the wrong
    // reason (a dialog that never re-rendered at all).
    let closed = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadTenantDashboard') {
        await fulfillJSON(route, dashboardResponse(environment, { reviews: [REVIEW] }));
        return;
      }
      if (body.method === 'LoadReviewDetail') {
        await fulfillJSON(route, {
          reviewId: REVIEW.reviewId,
          review: { ...REVIEW, status: closed ? 'CLOSED' : REVIEW.status },
          canComment: true,
          canClose,
        });
        return;
      }
      if (body.method === 'CloseReview') {
        closed = true;
        await fulfillJSON(route, { ...REVIEW, status: 'CLOSED' });
        return;
      }
      await route.continue();
    });

    await openDashboardReviewsTab(app, environment);
    await app.tenantDashboard.openReview('Add widget');
    await app.reviewDetailDialog.waitForOpen();
  }

  test('closing a review requires confirmation and reflects the closed status', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-close');
    try {
      await openReviewWithClosePermission(app, page, environment, true);

      const dialog = app.reviewDetailDialog;
      await expect(dialog.closeReviewButton()).toBeVisible();
      await dialog.closeReviewButton().click();
      await expect(dialog.confirmCloseButton()).toBeVisible();
      await dialog.confirmCloseButton().click();

      await expect(dialog.locator()).toContainText('CLOSED');
      await expect(dialog.closeReviewButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the missing access instead of rendering a close action that would fail', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-close-restricted');
    try {
      await openReviewWithClosePermission(app, page, environment, false);

      const dialog = app.reviewDetailDialog;
      await expect(dialog.closeReviewButton()).toHaveCount(0);
      await expect(dialog.closeRestrictedNote()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

test.describe('tenant dashboard — advancing the merge queue (#1348)', () => {
  test('advancing the queue requires confirmation and reports the merged review', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('queue-advance');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(
            route,
            dashboardResponse(environment, { canAdvanceMergeQueue: true, mergeQueue: [REVIEW] }),
          );
          return;
        }
        if (body.method === 'AdvanceMergeQueue') {
          await fulfillJSON(route, { ...REVIEW, status: 'MERGED' });
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment, 'Merge queue');
      await expect(app.tenantDashboard.advanceMergeQueueButton()).toBeVisible();
      await app.tenantDashboard.advanceMergeQueueButton().click();
      await expect(app.tenantDashboard.advanceMergeQueueConfirmButton()).toBeVisible();
      await app.tenantDashboard.advanceMergeQueueConfirmButton().click();

      await expect(app.tenantDashboard.advanceMergeQueueConfirmButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the reason instead of vanishing when the queue spans two target branches', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('queue-advance-mixed');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(
            route,
            dashboardResponse(environment, {
              canAdvanceMergeQueue: true,
              mergeQueue: [
                REVIEW,
                { ...REVIEW, reviewId: 'review-2', name: 'Add gadget', targetBranch: 'release' },
              ],
            }),
          );
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment, 'Merge queue');
      await expect(app.tenantDashboard.advanceMergeQueueButton()).toHaveCount(0);
      await expect(app.tenantDashboard.advanceMergeQueueMixedBranchNote()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('hides the Advance queue action and names the missing access when the caller cannot advance it', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('queue-advance-restricted');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(
            route,
            dashboardResponse(environment, { canAdvanceMergeQueue: false, mergeQueue: [REVIEW] }),
          );
          return;
        }
        await route.continue();
      });

      await openDashboardReviewsTab(app, environment, 'Merge queue');
      await expect(app.tenantDashboard.advanceMergeQueueButton()).toHaveCount(0);
      await expect(app.tenantDashboard.advanceMergeQueueRestrictedNote()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

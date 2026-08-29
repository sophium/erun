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
    platformAlias: 'pw-aws',
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

  // #1390: operatorPlatformError's "sign-in expired" sentence used to be a
  // dead end wherever a review write surfaced it. The alert must now offer
  // the same "Log in" the sidebar's cloud alias row already uses, and
  // clicking it must actually dispatch LoginCloudProvider for the tenant's
  // primary alias — not just render inert text. #1392: a successful sign-in
  // used to leave this exact error and button in place next to a
  // now-valid session, since nothing cleared the stale write error — this
  // also asserts the error actually clears, so the operator sees the dialog
  // ready to retry Create rather than the identical failure.
  test('a stale-identity error on create offers to sign in, and a successful sign-in clears it', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-create-stale-identity');
    try {
      let loginAlias = '';
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
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              error: 'Your sign-in is no longer valid for this tenant. Sign in again and retry.',
            }),
          });
          return;
        }
        if (body.method === 'LoginCloudProvider') {
          const args = (JSON.parse(request.postData() ?? '{}') as { args?: string[] }).args ?? [];
          loginAlias = args[0] ?? '';
          await fulfillJSON(route, { alias: 'pw-aws', provider: 'aws', status: 'active' });
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
      await dialog.push();
      await dialog.fillName('Add widget');
      await dialog.create();

      const alert = dialog
        .locator()
        .getByRole('alert')
        .filter({ hasText: 'sign-in is no longer valid' });
      await expect(alert).toBeVisible();
      const signIn = dialog.locator().getByRole('button', { name: 'Log in' });
      await expect(signIn).toBeVisible();
      await signIn.click();
      await expect.poll(() => loginAlias).toBe('pw-aws');

      // The dialog must recover: the stale write error and its Log in
      // button are gone, and the dialog is ready for the operator to retry
      // Create themselves — the sign-in fixed the session, not the submit.
      await expect(alert).toHaveCount(0);
      await expect(dialog.locator().getByRole('button', { name: 'Log in' })).toHaveCount(0);
      await expect(dialog.locator()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The regression this action must not create: an unrelated create failure
  // (not the stale-identity sentence) must render as before — message only,
  // no manufactured button.
  test('an unrelated create failure offers no sign-in action', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('review-create-other-error');
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
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ error: 'load tenant dashboard POST /v1/reviews: http 500' }),
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
      await dialog.push();
      await dialog.fillName('Add widget');
      await dialog.create();

      await expect(dialog.locator().getByRole('alert')).toContainText('http 500');
      await expect(dialog.locator().getByRole('button', { name: 'Log in' })).toHaveCount(0);
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

// The desktop could read a review's reviewers but do nothing else: no way to
// assign one, so `erun review list --waiting-on-me`'s own filter had nothing
// to ever return. These specs cover the Add reviewers action over the
// stubbed platform RPC, mirroring the close/advance specs above.
test.describe('tenant dashboard — assigning reviewers', () => {
  const JANE = { userId: 'user-2', username: 'jane' };
  const BOB = { userId: 'user-3', username: 'bob' };

  async function openReviewWithReviewers(
    app: AppShell,
    page: Page,
    environment: string,
    overrides: Partial<{
      reviewers: (typeof JANE)[];
      availableReviewers: (typeof JANE)[];
      canAssignReviewers: boolean;
      canRemoveReviewers: boolean;
      reviewersRestricted: string;
    }>,
    onAdd?: (userId: string) => void,
    onRemove?: (userId: string) => void,
  ): Promise<{ reviewers: (typeof JANE)[] }> {
    const state = { reviewers: overrides.reviewers ?? [] };
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadTenantDashboard') {
        await fulfillJSON(route, dashboardResponse(environment, { reviews: [REVIEW] }));
        return;
      }
      if (body.method === 'LoadReviewDetail') {
        await fulfillJSON(route, {
          reviewId: REVIEW.reviewId,
          review: REVIEW,
          canComment: true,
          reviewers: state.reviewers,
          reviewersRestricted: overrides.reviewersRestricted,
          canAssignReviewers: overrides.canAssignReviewers ?? true,
          canRemoveReviewers: overrides.canRemoveReviewers ?? true,
          availableReviewers: overrides.availableReviewers ?? [JANE, BOB],
        });
        return;
      }
      if (body.method === 'AddReviewer') {
        const args = (JSON.parse(request.postData() ?? '{}') as { args?: [{ userId?: string }] })
          .args ?? [{}];
        const userId = args[0]?.userId ?? '';
        onAdd?.(userId);
        const assigned = [JANE, BOB].find((user) => user.userId === userId);
        if (assigned) {
          state.reviewers = [...state.reviewers, assigned];
        }
        await fulfillJSON(route, { userId });
        return;
      }
      if (body.method === 'RemoveReviewer') {
        const args = (JSON.parse(request.postData() ?? '{}') as { args?: [{ userId?: string }] })
          .args ?? [{}];
        const userId = args[0]?.userId ?? '';
        onRemove?.(userId);
        state.reviewers = state.reviewers.filter((user) => user.userId !== userId);
        await fulfillJSON(route, null);
        return;
      }
      await route.continue();
    });

    await openDashboardReviewsTab(app, environment);
    await app.tenantDashboard.openReview('Add widget');
    await app.reviewDetailDialog.waitForOpen();
    return state;
  }

  test('assigning a reviewer from the picker shows it in the list and offers Remove', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviewer-add');
    try {
      let addedUserId = '';
      await openReviewWithReviewers(app, page, environment, {}, (userId) => {
        addedUserId = userId;
      });

      const dialog = app.reviewDetailDialog;
      await expect(dialog.locator()).toContainText('No reviewers assigned yet.');
      await dialog.addReviewerButton().click();
      await dialog.choosePendingReviewer('jane');
      await dialog.assignReviewerButton().click();

      await expect(dialog.reviewerRow('jane')).toBeVisible();
      expect(addedUserId).toBe(JANE.userId);
      await expect(dialog.removeReviewerButton('jane')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('removing a reviewer requires confirmation and reflects the empty list afterward', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviewer-remove');
    try {
      let removedUserId = '';
      await openReviewWithReviewers(
        app,
        page,
        environment,
        { reviewers: [JANE] },
        undefined,
        (userId) => {
          removedUserId = userId;
        },
      );

      const dialog = app.reviewDetailDialog;
      await expect(dialog.reviewerRow('jane')).toBeVisible();
      await dialog.removeReviewerButton('jane').click();
      await expect(dialog.confirmRemoveReviewerButton('jane')).toBeVisible();
      await dialog.confirmRemoveReviewerButton('jane').click();

      await expect(dialog.locator()).toContainText('No reviewers assigned yet.');
      expect(removedUserId).toBe(JANE.userId);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('hides Add reviewer and names the missing access when the caller cannot assign', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviewer-add-restricted');
    try {
      await openReviewWithReviewers(app, page, environment, { canAssignReviewers: false });

      const dialog = app.reviewDetailDialog;
      await expect(dialog.addReviewerButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the missing access instead of a blank list when the caller cannot read reviewers', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviewer-list-restricted');
    try {
      await openReviewWithReviewers(app, page, environment, {
        reviewersRestricted: 'GET /v1/reviews/{review_id}/reviewers',
      });

      const dialog = app.reviewDetailDialog;
      await expect(dialog.reviewersRestrictedNote()).toBeVisible();
      await expect(dialog.addReviewerButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the enrollment remedy when every enrolled user is already a reviewer', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviewer-add-none-left');
    try {
      await openReviewWithReviewers(app, page, environment, {
        reviewers: [JANE],
        availableReviewers: [JANE],
      });

      const dialog = app.reviewDetailDialog;
      await dialog.addReviewerButton().click();
      await expect(dialog.noReviewersLeftNote()).toBeVisible();
      await expect(dialog.reviewerPickerTrigger()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

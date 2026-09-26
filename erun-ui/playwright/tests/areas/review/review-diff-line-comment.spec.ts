import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Starting a new top-level review thread from a diff line is what
// ReviewDetailDialog.Comments.tsx used to defer for exactly this reason: only
// the diff panel knows which line was clicked. The Go side
// (CreateReviewComment) is covered by erun-ui/tenant_review_write_test.go;
// these specs cover the desktop's own precondition gating and the happy path.
//
// Everything the diff panel offers is rendered from LoadDiff's answer, and a
// locator assertion carries no clock of its own: `toHaveCSS`, `toBeVisible`,
// `toHaveAttribute` and `toHaveCount` resolve to expect's 10s default rather
// than the budget these tests declare. Under contention a read that is merely
// slow therefore reds the step with the test's clock unspent — the class
// fixtures/erunApp.ts's `withTestBudget` exists for, and what the held-read
// case at the end of this file reproduces.

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const DIFF = {
  rawDiff: '',
  workingDirectory: '/seed',
  summary: { fileCount: 1, additions: 1, deletions: 0 },
  files: [
    {
      path: 'main.go',
      status: 'modified',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [
        {
          header: '@@ -1,0 +1,1 @@',
          lines: [{ kind: 'add', oldLine: null, newLine: 1, content: 'package main' }],
        },
      ],
    },
  ],
  tree: [{ name: 'main.go', path: 'main.go', type: 'file', depth: 0 }],
  reviewCommits: [
    {
      hash: 'abc123',
      shortHash: 'abc123',
      subject: 'Add main',
      author: 'operator',
      date: '2026-01-01T00:00:00Z',
    },
  ],
  scope: 'all',
  includesWorktree: false,
};

const REVIEW = {
  reviewId: 'review-1',
  tenantId: 't1',
  name: 'Add widget',
  targetBranch: 'main',
  sourceBranch: 'feature/widget',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

// DIFF_NO_COMMIT is DIFF with no commit for a new thread to anchor to, so
// resolveDiffCommitHash resolves to '' and the "commit this change" blocked
// reason renders instead of the "no active review" one.
const DIFF_NO_COMMIT = { ...DIFF, reviewCommits: [] };

function routeLoadTenantDashboard(
  route: Route,
  tenant: string,
  environment: string,
  reviews: (typeof REVIEW)[],
): Promise<void> {
  return fulfillJSON(route, {
    tenant,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    reviews,
    panels: [{ tab: 'users' }, { tab: 'reviews' }],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
  });
}

// dismissAIOccupancyPromptIfShown is a defensive wait, not a feature this
// spec is testing: opening an environment auto-spawns its AI tab
// (ensureDefaultEnvTabs), which resolves either into an AI tab or -- if the
// environment's activity lease is already held -- the occupancy prompt
// (erun#1221). These tests care about the diff panel, not the AI tab, so race
// on whichever the spawn actually produces and clear the prompt out of the
// way rather than letting it block a later click.
async function dismissAIOccupancyPromptIfShown(
  app: import('../../../pages/index.js').AppShell,
): Promise<void> {
  const dialog = app.aiOccupancyPromptDialog;
  await Promise.race([
    dialog.waitForOpen().catch(() => undefined),
    app.page
      .getByRole('tab', { name: 'AI', exact: true })
      .waitFor({ state: 'visible' })
      .catch(() => undefined),
  ]);
  if (await dialog.locator().isVisible()) {
    await dialog.cancel();
    await dialog.waitForClosed();
  }
}

// openActiveReviewContext opens a review from the tenant dashboard and closes
// its (modal) dialog, establishing reviewId as the active commenting context
// per closeReviewDetail's own comment, then returns to the environment's diff
// panel -- the same round trip the "starts a new top-level thread" test below
// exercises, factored out so the two blocked-reason tests can set up the same
// active-review precondition without duplicating it.
async function openActiveReviewContext(
  app: import('../../../pages/index.js').AppShell,
  tenant: string,
  environment: string,
): Promise<void> {
  await waitForSeededRow(app, tenant, environment);
  await app.sidebar.openTenantDashboard(tenant);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Reviews');
  await app.tenantDashboard.openReview('Add widget');
  await app.reviewDetailDialog.waitForOpen();
  await app.reviewDetailDialog.locator().getByRole('button', { name: 'Close' }).click();
  await app.reviewDetailDialog.waitForClosed();
  await app.sidebar.openEnvironment(tenant, environment);
  await dismissAIOccupancyPromptIfShown(app);
}

test.describe('diff panel — commenting on a line (#1348, #1388)', () => {
  test('offers to open the Reviews tab when no review is in context', async ({
    app,
    page,
    seededEnv,
  }) => {
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'LoadTenantDashboard') {
        await routeLoadTenantDashboard(route, seededEnv.tenant, seededEnv.environment, []);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    // Converge on the panel having opened, then on the diff it fetches: both are
    // separate renders after the toggle, and expect's own budget is a fixed 10s.
    await app.reviewPanel.waitForOpen();
    await app.page.getByText('package main').waitFor({ state: 'visible' });

    // The affordance is revealed by hovering its own line rather than painted
    // on every row: the diff is the densest reading surface in the app, so a
    // persistent per-line icon would compete with the code it discusses.
    const action = app.page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());
    await app.page.getByText('package main').hover();
    // The reveal is the hover's own render, so it converges on this test's
    // declared budget rather than on expect's 10s default.
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    await action.click();
    await expect(
      app.page.getByText('Open a review from the Reviews tab to comment on this line.'),
    ).toBeVisible(withTestBudget());

    // Unlike the other two blocked reasons (checked below), this one names a
    // destination on a different surface -- so the popover carries a real
    // action to get there in one click, rather than leaving the reader to
    // find the tenant dashboard on their own (#1388).
    const openReviewsTab = app.page.getByRole('button', { name: 'Open Reviews tab' });
    await expect(openReviewsTab).toBeVisible(withTestBudget());
    await openReviewsTab.click();

    await app.tenantDashboard.waitForOpen();
    await expect(app.tenantDashboard.tab('Reviews')).toHaveAttribute(
      'aria-selected',
      'true',
      withTestBudget(),
    );
  });

  test('still explains — with no action — that the change needs a commit first', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('diff-comment-no-commit');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF_NO_COMMIT);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, { reviewId: REVIEW.reviewId, review: REVIEW, canComment: true });
          return;
        }
        await route.continue();
      });

      // A review is the active commenting context throughout -- this blocked
      // reason is about the diff's own commit state, not the review.
      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      // Converge on the panel having opened, then on the diff it fetches: both
      // are separate renders after the toggle, and expect's own budget is fixed.
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await expect(page.getByText('Commit this change before commenting on it.')).toBeVisible(
        withTestBudget(),
      );
      // This blocked reason is actionable exactly where the operator already
      // stands (commit the change), so it stays message-only (#1388).
      await expect(page.getByRole('button', { name: 'Open Reviews tab' })).toHaveCount(0);
      await expect(page.getByLabel('New comment')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('still explains — with no action — a lack of comment access', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('diff-comment-no-access');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: REVIEW,
            canComment: false,
          });
          return;
        }
        await route.continue();
      });

      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      // Converge on the panel having opened, then on the diff it fetches: both
      // are separate renders after the toggle, and expect's own budget is fixed.
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await expect(page.getByText('You do not have access to comment on this review.')).toBeVisible(
        withTestBudget(),
      );
      // Actionable exactly where the operator stands (ask for access), so no
      // navigation button here either (#1388).
      await expect(page.getByRole('button', { name: 'Open Reviews tab' })).toHaveCount(0);
      await expect(page.getByLabel('New comment')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('starts a new top-level thread anchored to the clicked line', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('diff-comment');
    try {
      let commentInput: Record<string, unknown> | null = null;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, { reviewId: REVIEW.reviewId, review: REVIEW, canComment: true });
          return;
        }
        if (body.method === 'CreateReviewComment') {
          const parsed = JSON.parse(request.postData() ?? '{}') as {
            args: [Record<string, unknown>];
          };
          commentInput = parsed.args[0];
          await fulfillJSON(route, {
            commentId: 'comment-1',
            creatorUserId: 'operator',
            status: 'OPEN',
            commitId: 'abc123',
            filePath: 'main.go',
            line: 1,
            body: 'what does this do?',
          });
          return;
        }
        await route.continue();
      });

      // Open the review to establish it as the active commenting context,
      // then close the (modal) dialog so the diff panel underneath it is
      // reachable — see closeReviewDetail's own comment for why this
      // survives the close.
      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      // Converge on the panel having opened, then on the diff it fetches: both
      // are separate renders after the toggle, and expect's own budget is fixed.
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await page.getByLabel('New comment').fill('what does this do?');
      await page.getByRole('button', { name: 'Comment', exact: true }).click();

      // The composer closes when CreateReviewComment's own answer lands, so
      // this convergence waits on the round trip, not on expect's 10s default.
      await expect(page.getByLabel('New comment')).toHaveCount(0, withTestBudget());
      expect(commentInput).toMatchObject({
        reviewId: REVIEW.reviewId,
        commitId: 'abc123',
        filePath: 'main.go',
        line: 1,
        body: 'what does this do?',
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The line-comment affordance exists only once the panel has rendered the
  // diff LoadDiff answered with, and the assertions on it carry no timeout of
  // their own: `toHaveCSS` resolves to expect's 10s default rather than the
  // budget this test declares. A read that is merely slow therefore reds the
  // step with the test's own clock unspent.
  //
  // The hold below is deliberately just past that 10s default: the smallest
  // delay that discriminates. It is injected at a named RPC (this spec's own
  // LoadDiff stub) rather than by loading the machine, so the reproduction is
  // deterministic on a quiet host. Pre-fix this case reds at exactly 10_000ms
  // with 20s of its own budget unused.
  test('a diff read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    // 60s, not the suite's 30s default: this case deliberately spends 12s of
    // its own budget holding the read above, so the default is not a clock for
    // the scenario -- it is a clock for the scenario minus the delay this case
    // exists to introduce. Same pairing the sibling held-read cases use.
    test.setTimeout(60_000);
    let held = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        if (!held) {
          held = true;
          await new Promise((resolve) => setTimeout(resolve, 12_000));
        }
        await fulfillJSON(route, DIFF);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();

    const action = app.page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());
  });
});

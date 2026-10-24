import type { Request, Route } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The chip must never show a confirmed answer before DiffReviewStatus resolves,
// and its action must reuse the Reviews tab's write paths. Reads carry
// withTestBudget() because locator assertions default to expect's 10s.

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

function invokeArgs(request: Request): Record<string, unknown> {
  const parsed = JSON.parse(request.postData() ?? '{}') as { args: [Record<string, unknown>] };
  return parsed.args[0];
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
  reviewBase: { branch: 'main', commit: 'def456', shortCommit: 'def456' },
  reviewCommits: [],
  scope: 'all',
  includesWorktree: false,
};

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

async function openDiffPanel(
  app: import('../../../pages/index.js').AppShell,
  tenant: string,
  environment: string,
): Promise<void> {
  await app.sidebar.openEnvironment(tenant, environment);
  await dismissAIOccupancyPromptIfShown(app);
  await app.titlebar.toggleReviewPanel();
  await app.reviewPanel.waitForOpen();
  await app.page.getByText('package main').waitFor({ state: 'visible' });
}

test.describe('diff panel — review-status chip', () => {
  test('never claims "No review" before the platform read resolves, and reflects the confirmed answer once it does', async ({
    app,
    page,
    seededEnv,
  }) => {
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let releaseDiffReviewStatus: (() => void) | undefined;
    const diffReviewStatusGate = new Promise<void>((resolve) => {
      releaseDiffReviewStatus = resolve;
    });
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        await diffReviewStatusGate;
        await fulfillJSON(route, { state: 'none', canAdvanceMergeQueue: true });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Checking status…')).toBeVisible(
      withTestBudget(),
    );
    await expect(app.reviewPanel.reviewStatusChip(envKey, 'No review')).toHaveCount(
      0,
      withTestBudget(),
    );
    // The dialog itself resolves whether this caller may create a review.
    await expect(app.reviewPanel.reviewActionButton(envKey, 'Start a review')).toBeVisible(
      withTestBudget(),
    );

    releaseDiffReviewStatus?.();

    await expect(app.reviewPanel.reviewStatusChip(envKey, 'No review')).toBeVisible(
      withTestBudget(),
    );
    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Checking status…')).toHaveCount(
      0,
      withTestBudget(),
    );
    await expect(app.reviewPanel.reviewActionButton(envKey, 'Start a review')).toBeVisible(
      withTestBudget(),
    );
  });

  // loadDiffReviewStatus has no in-flight guard, so an unstable dependency would
  // loop silently; the read count is the only observable.
  test('reads the platform once for a settled panel, and a diff refresh does not re-read', async ({
    app,
    page,
    seededEnv,
  }) => {
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let statusReads = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        statusReads += 1;
        await fulfillJSON(route, { state: 'none', canAdvanceMergeQueue: false });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    await expect(app.reviewPanel.reviewStatusChip(envKey, 'No review')).toBeVisible(
      withTestBudget(),
    );

    // The env and branch are unchanged, so a refresh must not re-read status.
    await app.reviewPanel.refreshDiff();
    await expect
      .poll(() => statusReads, { message: 'one read per environment and branch' })
      .toBe(1);
  });

  test('a READY review with a queue position offers Advance queue, reusing the merge-queue write', async ({
    app,
    page,
    seededEnv,
  }) => {
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let advanceInput: Record<string, unknown> | null = null;
    let diffReviewStatusCalls = 0;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        diffReviewStatusCalls += 1;
        if (diffReviewStatusCalls === 1) {
          await fulfillJSON(route, {
            state: 'ready',
            reviewId: 'review-1',
            name: 'Add widget',
            queuePosition: 2,
            canAdvanceMergeQueue: true,
          });
          return;
        }
        // Never an optimistic client guess.
        await fulfillJSON(route, {
          state: 'merging',
          reviewId: 'review-1',
          name: 'Add widget',
          canAdvanceMergeQueue: true,
        });
        return;
      }
      if (body.method === 'AdvanceMergeQueue') {
        advanceInput = invokeArgs(request);
        await fulfillJSON(route, {
          reviewId: 'review-1',
          name: 'Add widget',
          status: 'MERGE',
          targetBranch: 'main',
        });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Ready · queued #2')).toBeVisible(
      withTestBudget(),
    );
    await app.reviewPanel.reviewActionButton(envKey, 'Advance queue').click();
    await app.reviewPanel.reviewActionButton(envKey, 'Confirm').click();

    // Polled: the route handler may record the call after the click resolves.
    await expect
      .poll(() => advanceInput)
      .toMatchObject({ tenant: seededEnv.tenant, targetBranch: 'main' });
    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Merging')).toBeVisible(withTestBudget());
  });

  test('a blocked review disables advancing and routes to the discussion', async ({
    app,
    page,
    seededEnv,
  }) => {
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let reviewDetailInput: Record<string, unknown> | null = null;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        await fulfillJSON(route, {
          state: 'blocked',
          reviewId: 'review-1',
          name: 'Add widget',
          unresolvedThreads: 2,
          canAdvanceMergeQueue: true,
        });
        return;
      }
      if (body.method === 'LoadReviewDetail') {
        reviewDetailInput = invokeArgs(request);
        await fulfillJSON(route, {
          reviewId: 'review-1',
          review: {
            reviewId: 'review-1',
            name: 'Add widget',
            targetBranch: 'main',
            sourceBranch: 'feature/x',
            status: 'READY',
          },
          canComment: true,
        });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Blocked · 2 threads')).toBeVisible(
      withTestBudget(),
    );
    await expect(app.reviewPanel.reviewActionButton(envKey, 'Advance queue')).toHaveCount(
      0,
      withTestBudget(),
    );
    await app.reviewPanel.reviewActionButton(envKey, 'Resolve 2 threads').click();

    await app.reviewDetailDialog.waitForOpen();
    expect(reviewDetailInput).toMatchObject({ tenant: seededEnv.tenant, reviewId: 'review-1' });
  });

  test('clicking the chip itself opens the review, the same navigation a Reviews-tab row uses', async ({
    app,
    page,
    seededEnv,
  }) => {
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let reviewDetailInput: Record<string, unknown> | null = null;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        await fulfillJSON(route, {
          state: 'open',
          reviewId: 'review-1',
          name: 'Add widget',
          canAdvanceMergeQueue: false,
        });
        return;
      }
      if (body.method === 'LoadReviewDetail') {
        reviewDetailInput = invokeArgs(request);
        await fulfillJSON(route, {
          reviewId: 'review-1',
          review: {
            reviewId: 'review-1',
            name: 'Add widget',
            targetBranch: 'main',
            sourceBranch: 'feature/x',
            status: 'OPEN',
          },
        });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    await expect(app.reviewPanel.reviewStatusChip(envKey, 'Open · building')).toBeVisible(
      withTestBudget(),
    );
    await expect(app.reviewPanel.reviewActionButton(envKey, 'View review')).toBeVisible(
      withTestBudget(),
    );
    await app.reviewPanel.reviewStatusChip(envKey, 'Open · building').click();

    await app.reviewDetailDialog.waitForOpen();
    expect(reviewDetailInput).toMatchObject({ tenant: seededEnv.tenant, reviewId: 'review-1' });
  });

  // The read is held past expect's 10s default, released relative to the
  // assertion start so it cannot arrive early.
  test('a status read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const envKey = `${seededEnv.tenant}/${seededEnv.environment}`;
    let releaseStatusRead: () => void = () => undefined;
    const statusReadHeld = new Promise<void>((resolve) => {
      releaseStatusRead = resolve;
    });
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/x' });
        return;
      }
      if (body.method === 'DiffReviewStatus') {
        await statusReadHeld;
        await fulfillJSON(route, { state: 'none', canAdvanceMergeQueue: true });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    setTimeout(releaseStatusRead, 12_000);
    await expect(app.reviewPanel.reviewStatusChip(envKey, 'No review')).toBeVisible(
      withTestBudget(),
    );
  });
});

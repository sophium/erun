import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// Starting a review from the diff panel: the panel already knows the
// environment and the branch it is diffing against, so opening "Open a
// review" from here must carry both instead of sending the operator to the
// Reviews tab to re-specify what they were already looking at. Commenting on
// a diff line is a separate, already-shipped entry point
// (DiffList.CommentAction). The dialog and its write thunks (commit, push,
// create) are unchanged — covered by tenant-dashboard-review-write.spec.ts
// and erun-ui/tenant_review_write_test.go — these specs cover only the new
// entry point, its prefill, and its own capability probe
// (erun-ui/tenant_review_capability_test.go covers that Go method directly).

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

// reviewBase.branch ('release/2.0') is a deliberately unusual value, distinct
// from the dialog's own 'main' fallback default — so a passing "the target
// branch is prefilled" assertion cannot be mistaken for the fallback firing
// by coincidence.
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
  reviewBase: { branch: 'release/2.0', commit: 'def456', shortCommit: 'def456' },
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
  targetBranch: 'release/2.0',
  sourceBranch: 'feature/777-thing',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

// dismissAIOccupancyPromptIfShown mirrors review-diff-line-comment.spec.ts's
// own helper (not imported: specs stay independent of each other's internals,
// see erun-ui/playwright/AGENTS.md). Opening an environment auto-spawns its AI
// tab, which resolves either into an AI tab or -- if the environment's
// activity lease is already held -- this occupancy prompt. These
// specs care about the diff panel, not the AI tab, so race on whichever the
// spawn actually produces.
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
  await expect(app.page.getByText('package main')).toBeVisible();
}

function startReviewButton(app: import('../../../pages/index.js').AppShell) {
  return app.page.getByRole('button', { name: 'Start a review' });
}

test.describe('diff panel — starting a review (#1315)', () => {
  test('opens the dialog prefilled from the diff panel, with nothing retyped', async ({
    app,
    page,
    seededEnv,
  }) => {
    let createInput: Record<string, unknown> | null = null;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'TenantReviewCreateCapability') {
        await fulfillJSON(route, { canCreate: true });
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/777-thing' });
        return;
      }
      if (body.method === 'ExecCommit') {
        await fulfillJSON(route, {
          branch: 'feature/777-thing',
          commit: 'abc123',
          files: ['a.go'],
        });
        return;
      }
      if (body.method === 'ExecPush') {
        await fulfillJSON(route, {
          branch: 'feature/777-thing',
          remote: 'origin',
          commit: 'abc123',
        });
        return;
      }
      if (body.method === 'CreateReview') {
        const parsed = JSON.parse(request.postData() ?? '{}') as {
          args: [Record<string, unknown>];
        };
        createInput = parsed.args[0];
        await fulfillJSON(route, REVIEW);
        return;
      }
      if (body.method === 'LoadReviewDetail') {
        await fulfillJSON(route, { reviewId: REVIEW.reviewId, review: REVIEW, canComment: true });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);

    await expect(startReviewButton(app)).toBeVisible();
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    // The environment and its current branch are the diff panel's own
    // context, read back (EnvironmentWorkingIssue), never typed by this test.
    await expect(dialog.locator()).toContainText(`${seededEnv.tenant} / ${seededEnv.environment}`);
    await expect(dialog.locator()).toContainText('feature/777-thing');
    // The target branch is the diff's own merge target (reviewBase.branch),
    // prefilled before this test has interacted with the field at all.
    await expect(dialog.targetBranchInput()).toHaveValue('release/2.0');

    // The review name is the one value the product cannot know on the
    // operator's behalf — everything else in this flow is either read back
    // from the environment or prefilled from the diff.
    await dialog.fillName('Add widget');
    await dialog.fillCommitMessage('describe the change');
    await dialog.commit();
    await dialog.push();
    await expect(dialog.locator()).toContainText('Pushed to origin/feature/777-thing');

    await dialog.create();
    await dialog.waitForClosed();

    expect(createInput).toMatchObject({
      tenant: seededEnv.tenant,
      name: 'Add widget',
      targetBranch: 'release/2.0',
      sourceBranch: 'feature/777-thing',
    });
    await app.reviewDetailDialog.waitForOpen();
    // The detail dialog must resolve the review it just created, not just
    // open — this entry point reaches openReviewDetail without the tenant
    // dashboard ever having loaded (unlike the Reviews tab's own New review
    // button), so its caller-context resolution needs the tenant threaded
    // through explicitly or it renders "No tenant is open." instead of data.
    await expect(app.reviewDetailDialog.locator()).toContainText('Add widget');
    await expect(app.reviewDetailDialog.locator()).not.toContainText('No tenant is open');
  });

  test('a push that fails names its own next action', async ({ app, page, seededEnv }) => {
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'TenantReviewCreateCapability') {
        await fulfillJSON(route, { canCreate: true });
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/777-thing' });
        return;
      }
      if (body.method === 'ExecCommit') {
        await fulfillJSON(route, {
          branch: 'feature/777-thing',
          commit: 'abc123',
          files: ['a.go'],
        });
        return;
      }
      if (body.method === 'ExecPush') {
        await route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: 'remote rejected: non-fast-forward' }),
        });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    await dialog.fillName('Add widget');
    await dialog.fillCommitMessage('describe the change');
    await dialog.commit();
    await dialog.push();

    // The failure is named, not a raw wire error swallowed into a generic
    // message, and Push stays clickable so retrying is the visible next
    // action rather than a dead end.
    await expect(dialog.locator().getByRole('alert')).toContainText('non-fast-forward');
    await expect(dialog.locator().getByRole('button', { name: 'Push' })).toBeEnabled();
    await expect(dialog.locator()).not.toContainText('Pushed to origin/');
  });

  // No TenantReviewCreateCapability stub here: the seeded harness configures
  // no erun-type platform alias for any tenant, so the real backend method
  // (erun-ui/tenant_review_capability.go) genuinely resolves "not ready" —
  // the same real, unmocked outcome an operator sees before connecting a
  // tenant to a hosted platform.
  test('a restricted capability renders as restricted, not as an empty dialog', async ({
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
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/777-thing' });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    // The dialog still renders its ordinary content (title, push step,
    // prefilled branch) -- restricted is a state layered on top, not a blank
    // surface in its place.
    await expect(dialog.locator()).toContainText('feature/777-thing');
    await expect(dialog.locator().getByRole('status')).toContainText(
      "This tenant's platform connection isn't ready",
    );
    await expect(dialog.createButton()).toBeDisabled();
  });
});

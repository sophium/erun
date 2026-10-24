import type { Request, Route } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The panel already knows the env and diff base, so opening a review from here
// must carry both. Dialog reads carry withTestBudget() because locator
// assertions default to expect's 10s.

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

// Deliberately distinct from the dialog's 'main' fallback.
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

// Opening an env spawns its AI tab, or the occupancy prompt if its lease is
// held; race on whichever appears.
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

    await expect(startReviewButton(app)).toBeVisible(withTestBudget());
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    await expect(dialog.locator()).toContainText(
      `${seededEnv.tenant} / ${seededEnv.environment}`,
      withTestBudget(),
    );
    await expect(dialog.locator()).toContainText('feature/777-thing', withTestBudget());
    await expect(dialog.targetBranchInput()).toHaveValue('release/2.0', withTestBudget());

    await dialog.fillName('Add widget');
    await dialog.fillCommitMessage('describe the change');
    await dialog.commit();
    await dialog.push();
    await expect(dialog.locator()).toContainText(
      'Pushed to origin/feature/777-thing',
      withTestBudget(),
    );

    await dialog.create();
    await dialog.waitForClosed();

    expect(createInput).toMatchObject({
      tenant: seededEnv.tenant,
      name: 'Add widget',
      targetBranch: 'release/2.0',
      sourceBranch: 'feature/777-thing',
    });
    await app.reviewDetailDialog.waitForOpen();
    // This entry point never loads the tenant dashboard, so the tenant must be
    // threaded through or the detail renders "No tenant is open.".
    await expect(app.reviewDetailDialog.locator()).toContainText('Add widget', withTestBudget());
    await expect(app.reviewDetailDialog.locator()).not.toContainText('No tenant is open');
  });

  test('a caller who may not open a review is handed the grant that lifts it', async ({
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
      if (body.method === 'TenantReviewCreateCapability') {
        await fulfillJSON(route, {
          canCreate: false,
          restricted: 'You do not have access to create reviews.',
          accessRemedy: {
            command: 'erun platform user grant-role --user-id user-1 --role-id role-author',
            roleName: 'Author',
          },
        });
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/777-thing' });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    await expect(startReviewButton(app)).toBeVisible(withTestBudget());
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    await expect(dialog.locator()).toContainText(
      'You do not have access to create reviews.',
      withTestBudget(),
    );
    await expect(dialog.locator()).toContainText(
      'erun platform user grant-role --user-id user-1 --role-id role-author',
      withTestBudget(),
    );
    await expect(dialog.locator()).toContainText('Author', withTestBudget());
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

    await expect(dialog.locator().getByRole('alert')).toContainText(
      'non-fast-forward',
      withTestBudget(),
    );
    await expect(dialog.locator().getByRole('button', { name: 'Push' })).toBeEnabled(
      withTestBudget(),
    );
    await expect(dialog.locator()).not.toContainText('Pushed to origin/');
  });

  // Unstubbed: the harness configures no erun-type alias, so the real backend
  // resolves "not ready".
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
    // Restricted is layered on top of the ordinary content, not a blank dialog.
    await expect(dialog.locator()).toContainText('feature/777-thing', withTestBudget());
    await expect(dialog.locator().getByRole('status')).toContainText(
      "This tenant's platform connection isn't ready",
      withTestBudget(),
    );
    await expect(dialog.createButton()).toBeDisabled(withTestBudget());
  });

  // EnvironmentWorkingIssue is held just past expect's 10s default. It is armed
  // only as the dialog opens, since the panel reads the same RPC while booting.
  test('a branch read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    let armHold = false;
    let holdUntil = 0;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        if (armHold && holdUntil === 0) {
          holdUntil = Date.now() + 12_000;
        }
        const remaining = holdUntil - Date.now();
        if (remaining > 0) {
          await new Promise((resolve) => setTimeout(resolve, remaining));
        }
        await fulfillJSON(route, { available: true, branch: 'feature/777-thing' });
        return;
      }
      if (body.method === 'TenantReviewCreateCapability') {
        await fulfillJSON(route, { canCreate: true });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    armHold = true;
    await startReviewButton(app).click();

    const dialog = app.createReviewDialog;
    await dialog.waitForOpen();
    await expect(dialog.locator()).toContainText('feature/777-thing', withTestBudget());
  });
});

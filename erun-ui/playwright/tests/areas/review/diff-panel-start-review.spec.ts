import type { Request, Route } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

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
//
// Everything the dialog shows about the environment is the answer to a round
// trip it issues on open (EnvironmentWorkingIssue for the branch,
// TenantReviewCreateCapability for the permission, ExecCommit/ExecPush for
// each write step), and a locator assertion carries no clock of its own:
// `toContainText`, `toHaveValue` and `toBeDisabled` resolve to expect's 10s
// default rather than the budget these tests declare. Under contention a step
// that is merely slow therefore reds with the test's clock unspent — the class
// fixtures/erunApp.ts's `withTestBudget` exists for, and what the held-read
// case at the end of this file reproduces.

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
  // Converge on the panel having opened, then on the diff it fetches: both are
  // separate renders after the toggle, and expect's own budget is a fixed 10s.
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
    // The environment and its current branch are the diff panel's own
    // context, read back (EnvironmentWorkingIssue), never typed by this test —
    // so each read waits on the budget this test declared, not expect's 10s.
    await expect(dialog.locator()).toContainText(
      `${seededEnv.tenant} / ${seededEnv.environment}`,
      withTestBudget(),
    );
    await expect(dialog.locator()).toContainText('feature/777-thing', withTestBudget());
    // The target branch is the diff's own merge target (reviewBase.branch),
    // prefilled before this test has interacted with the field at all.
    await expect(dialog.targetBranchInput()).toHaveValue('release/2.0', withTestBudget());

    // The review name is the one value the product cannot know on the
    // operator's behalf — everything else in this flow is either read back
    // from the environment or prefilled from the diff.
    await dialog.fillName('Add widget');
    await dialog.fillCommitMessage('describe the change');
    await dialog.commit();
    await dialog.push();
    // The push badge is ExecPush's own answer, not a render the click started.
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
    // The detail dialog must resolve the review it just created, not just
    // open — this entry point reaches openReviewDetail without the tenant
    // dashboard ever having loaded (unlike the Reviews tab's own New review
    // button), so its caller-context resolution needs the tenant threaded
    // through explicitly or it renders "No tenant is open." instead of data.
    await expect(app.reviewDetailDialog.locator()).toContainText('Add widget', withTestBudget());
    await expect(app.reviewDetailDialog.locator()).not.toContainText('No tenant is open');
  });

  // The denied entry point, which the diff panel reaches without the tenant
  // dashboard ever having loaded. Naming the capability is not enough: the
  // notice has to hand over the grant, filled in with the caller's own user id.
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
    // The denial and its remedy are TenantReviewCreateCapability's answer, so
    // they land after the dialog opens rather than with it.
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

    // The failure is named, not a raw wire error swallowed into a generic
    // message, and Push stays clickable so retrying is the visible next
    // action rather than a dead end. The refusal is ExecPush's own answer.
    await expect(dialog.locator().getByRole('alert')).toContainText(
      'non-fast-forward',
      withTestBudget(),
    );
    await expect(dialog.locator().getByRole('button', { name: 'Push' })).toBeEnabled(
      withTestBudget(),
    );
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
    // surface in its place. Both the branch and the readiness notice are round
    // trips the dialog issues on open, and this spec leaves the capability
    // probe unstubbed, so it is the real backend's answer being waited on.
    await expect(dialog.locator()).toContainText('feature/777-thing', withTestBudget());
    await expect(dialog.locator().getByRole('status')).toContainText(
      "This tenant's platform connection isn't ready",
      withTestBudget(),
    );
    await expect(dialog.createButton()).toBeDisabled(withTestBudget());
  });

  // The environment and branch the dialog shows are EnvironmentWorkingIssue's
  // answer, and the assertions on them carry no timeout of their own:
  // `toContainText` resolves to expect's 10s default rather than the budget
  // this test declares. A read that is merely slow therefore reds the step
  // with the test's own clock unspent -- the class
  // fixtures/erunApp.ts's `withTestBudget` exists for.
  //
  // The hold below is deliberately just past that 10s default: the smallest
  // delay that discriminates. It is injected at a named RPC (this spec's own
  // EnvironmentWorkingIssue stub) rather than by loading the machine, so the
  // reproduction is deterministic on a quiet host. Pre-fix this case reds at
  // exactly 10_000ms with 20s of its own budget unused.
  //
  // The hold is armed only once the dialog is about to open, because the panel
  // reads the same RPC for its own header while it boots; holding that first
  // read would release this one before the dialog ever asked.
  test('a branch read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    // 60s, not the suite's 30s default: this case deliberately spends 12s of
    // its own budget holding the read above, so the default is not a clock for
    // the scenario -- it is a clock for the scenario minus the delay this case
    // exists to introduce. Same pairing the sibling held-read cases use.
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

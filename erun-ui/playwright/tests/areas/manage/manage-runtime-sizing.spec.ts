import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// Stubs LoadRuntimeSizing/ResizeRuntimeToRecommendation instead of falling
// through to the harness's stub kubectl (which has no cluster and so cannot
// exec `erun resize` into a pod). Mirrors manage-runtime-usage.spec.ts's
// stubRuntimeUsage pattern. LoadRuntimeSizing is stateful (reads `current`)
// so that a successful resize's invalidation-triggered refetch reflects the
// new state, exactly as the real in-pod command would on a second read.
async function stubRuntimeSizing(
  page: Page,
  options: {
    initial: unknown;
    resize?: (overrideLease: boolean) => unknown;
    holdMs?: number;
  },
): Promise<void> {
  let current = options.initial;
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args: unknown[] };
    if (body.method === 'LoadRuntimeSizing') {
      // holdMs makes this read land a fixed window after the request that
      // asked for it, so a step that is merely slow can be told apart from a
      // step that never converges. Anchored to the request (not to the stub's
      // own registration) so the delay is the same however long the dialog
      // took to open, and only the first read is held -- a refetch is a
      // separate step with its own convergence.
      if (options.holdMs && holdUntil === 0) {
        holdUntil = Date.now() + options.holdMs;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: current }),
      });
    }
    if (body.method === 'ResizeRuntimeToRecommendation' && options.resize) {
      const overrideLease = body.args[1] as boolean;
      const result = options.resize(overrideLease);
      if (result instanceof Error) {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: result.message }),
        });
      }
      current = result;
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: result }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog sizing recommendation panel', () => {
  test('applying the recommendation resizes without retyping the suggested values', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeSizing(app.page, {
      initial: {
        tenant,
        environment,
        available: true,
        actions: [
          { resource: 'cpu', from: '4', to: '6' },
          { resource: 'memory', from: '8916Mi', to: '12288Mi' },
        ],
      },
      resize: () => ({ tenant, environment, available: true, noOp: true }),
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeSizingPanel();
    await expect(panel).toBeVisible();
    await expect(app.manageDialog.runtimeSizingRefreshButton()).toBeVisible();

    // The exact suggested values render, so an operator can see what "Resize
    // to this" will apply before clicking it -- nothing here is retyped.
    // withTestBudget on the content reads, not expect's 10s default: each one
    // is the answer to a LoadRuntimeSizing round trip (and, below, to the
    // resize's refetch), which is a value this spec's own stub owns rather
    // than a render that has already happened -- see the held-read case at the
    // end of this file for the reproduction.
    await expect(panel).toContainText('cpu: 4 → 6', withTestBudget());
    await expect(panel).toContainText('memory: 8916Mi → 12288Mi', withTestBudget());

    await app.manageDialog.runtimeSizingApplyButton().click();

    // The invalidated query re-fetches, and the stub's post-resize response
    // (no actions left) is what the panel must reflect -- proof the click
    // actually drove the resize rather than only rendering a plan.
    await expect(panel).not.toContainText('cpu: 4 → 6', withTestBudget());
    await expect(app.manageDialog.runtimeSizingOverrideButton()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a resize held by another worker refuses and requires a deliberate override', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const refusal =
      'resize refused: this environment is held by orchestrator eng-42, user jane@example.com (lease "exec_job_attach") — a resize restarts the runtime pod and would interrupt that work; pass the override to resize anyway, or wait until it finishes';
    await stubRuntimeSizing(app.page, {
      initial: {
        tenant,
        environment,
        available: true,
        actions: [{ resource: 'cpu', from: '4', to: '6' }],
      },
      resize: (overrideLease) =>
        overrideLease ? { tenant, environment, available: true, actions: [] } : new Error(refusal),
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeSizingPanel();
    await app.manageDialog.runtimeSizingApplyButton().click();

    // The refusal names the holder, and the override affordance only appears
    // after that refusal -- it is never offered up front. Each of these is the
    // stub's own refusal text arriving over a round trip, so like the reads
    // above it waits on this test's clock rather than expect's 10s default.
    await expect(panel).toContainText('orchestrator eng-42', withTestBudget());
    await expect(panel).toContainText('user jane@example.com', withTestBudget());
    await expect(panel).toContainText('pass the override to resize anyway', withTestBudget());
    const overrideButton = app.manageDialog.runtimeSizingOverrideButton();
    await expect(overrideButton).toBeVisible();

    await overrideButton.click();

    // The explicit second click succeeds (the stub's overrideLease branch),
    // and the refusal clears.
    await expect(panel).not.toContainText('resize refused', withTestBudget());
    await expect(overrideButton).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The reported defect itself: a no-op ("Already sized as recommended") must
  // not go silent about why. This is exactly the operator complaint the fix
  // addresses -- a comfortable peak the shrink gate withholds because the
  // observed window is too short, previously invisible on this panel.
  test('a no-op recommendation still shows the evidence behind it', async ({ app, seededEnv }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeSizing(app.page, {
      initial: {
        tenant,
        environment,
        available: true,
        noOp: true,
        verdicts: [
          'memory insufficient-evidence from 23552Mi (peak 12153Mi of 23552Mi (52%), but only 1h12m observed of the 24h0m a shrink needs)',
          'cpu insufficient-evidence from 12 (0.00% of scheduling periods throttled (0 of 376556), but only 1h12m observed of the 24h0m a shrink needs)',
        ],
        evidence:
          '1h12m observed, 120 samples, 0 restarts, knob=runtimepod, from cgroup memory.peak, cgroup memory.events oom_kill, cgroup cpu.stat usage_usec/nr_throttled (not loadavg)',
      },
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeSizingPanel();
    await expect(panel).toContainText('Already sized as recommended.', withTestBudget());
    // The evidence line answers exactly what the bare verdict cannot: what
    // was measured, over what window, and why it falls short of a shrink.
    await expect(panel).toContainText(
      'only 1h12m observed of the 24h0m a shrink needs',
      withTestBudget(),
    );
    await expect(panel).toContainText('1h12m observed, 120 samples, 0 restarts', withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Unlike the two tests above, this one does not stub LoadRuntimeSizing, so
  // the seeded (inert, never-deployed) env's real probe runs against the
  // harness's stub kubectl and fails for real.
  //
  // The stub kubectl fails fast, so this exercises the classifier's "keep the
  // raw cause" branch, not the deadline-vs-external-kill classification --
  // this offline harness cannot make a probe actually time out or get
  // signal-killed. Those branches are covered by the Go suite instead
  // (erun-ui/runtime_probe_error_test.go and
  // TestLoadRuntimeSizingReportsOwnTimeoutNotSignalKilled /
  // TestLoadRuntimeSizingReportsExternalKillDistinctFromTimeout in
  // erun-ui/runtime_sizing_test.go).
  test('an unreachable pod fails soft with a stated reason, never a blank panel', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeSizingPanel();
    await expect(panel).toBeVisible();
    // The failure copy is this read's own outcome, so it waits on the read.
    await expect(panel).toContainText(
      "Cannot read this environment's sizing recommendation",
      withTestBudget(),
    );
    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The panel's content is the answer to LoadRuntimeSizing, and an assertion on
  // it carries no timeout of its own: `toContainText` has no waitFor
  // equivalent, so it resolves to expect's 10s default rather than the budget
  // this test declares. Under contention a read that is merely slow therefore
  // reds the step with the test's own clock unspent, which is how a loaded
  // machine turns into a failing branch nobody touched. The hold below is
  // deliberately just past that 10s default: the
  // smallest delay that discriminates, so the suite pays seconds here rather
  // than the tens a genuinely loaded machine would.
  //
  // Pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused; with the read pointed at that budget it passes at the read's real
  // arrival. The delay is injected at a named RPC (this spec's own
  // LoadRuntimeSizing stub) rather than by loading the machine, so the
  // reproduction is deterministic on a quiet host -- the same shape
  // sidebar-pom-convergence-budget.spec.ts uses for the POM steps.
  test('a recommendation that lands past the step cap is waited out, not cut off', async ({
    app,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await stubRuntimeSizing(app.page, {
      initial: {
        tenant,
        environment,
        available: true,
        actions: [
          { resource: 'cpu', from: '4', to: '6' },
          { resource: 'memory', from: '8916Mi', to: '12288Mi' },
        ],
      },
      holdMs: 12_000,
    });

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeSizingPanel();
    await expect(panel).toContainText('cpu: 4 → 6', withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

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
  },
): Promise<void> {
  let current = options.initial;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args: unknown[] };
    if (body.method === 'LoadRuntimeSizing') {
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
    await expect(panel).toContainText('cpu: 4 → 6');
    await expect(panel).toContainText('memory: 8916Mi → 12288Mi');

    await app.manageDialog.runtimeSizingApplyButton().click();

    // The invalidated query re-fetches, and the stub's post-resize response
    // (no actions left) is what the panel must reflect -- proof the click
    // actually drove the resize rather than only rendering a plan.
    await expect(panel).not.toContainText('cpu: 4 → 6');
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
    // after that refusal -- it is never offered up front.
    await expect(panel).toContainText('orchestrator eng-42');
    await expect(panel).toContainText('user jane@example.com');
    await expect(panel).toContainText('pass the override to resize anyway');
    const overrideButton = app.manageDialog.runtimeSizingOverrideButton();
    await expect(overrideButton).toBeVisible();

    await overrideButton.click();

    // The explicit second click succeeds (the stub's overrideLease branch),
    // and the refusal clears.
    await expect(panel).not.toContainText('resize refused');
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
    await expect(panel).toContainText('Already sized as recommended.');
    // The evidence line answers exactly what the bare verdict cannot: what
    // was measured, over what window, and why it falls short of a shrink.
    await expect(panel).toContainText('only 1h12m observed of the 24h0m a shrink needs');
    await expect(panel).toContainText('1h12m observed, 120 samples, 0 restarts');

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
    await expect(panel).toContainText("Cannot read this environment's sizing recommendation");
    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

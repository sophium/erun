import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// Stateful so a resize's refetch reflects the new state, as the real command would.
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
      // Anchored to the request so the delay is independent of dialog open time; only the first read is held.
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

    await expect(panel).toContainText('cpu: 4 → 6', withTestBudget());
    await expect(panel).toContainText('memory: 8916Mi → 12288Mi', withTestBudget());

    await app.manageDialog.runtimeSizingApplyButton().click();

    // The post-resize refetch proves the click actually drove the resize.
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

    // The override appears only after a refusal, never up front.
    await expect(panel).toContainText('orchestrator eng-42', withTestBudget());
    await expect(panel).toContainText('user jane@example.com', withTestBudget());
    await expect(panel).toContainText('pass the override to resize anyway', withTestBudget());
    const overrideButton = app.manageDialog.runtimeSizingOverrideButton();
    await expect(overrideButton).toBeVisible();

    await overrideButton.click();

    await expect(panel).not.toContainText('resize refused', withTestBudget());
    await expect(overrideButton).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

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
    await expect(panel).toContainText(
      'only 1h12m observed of the 24h0m a shrink needs',
      withTestBudget(),
    );
    await expect(panel).toContainText('1h12m observed, 120 samples, 0 restarts', withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Unstubbed: the real probe fails against the harness kubectl. Timeout vs.
  // signal-kill classification is covered in Go.
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
    await expect(panel).toContainText(
      "Cannot read this environment's sizing recommendation",
      withTestBudget(),
    );
    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The hold is just past expect's 10s default, so the read must be waited out on
  // the test's own budget.
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

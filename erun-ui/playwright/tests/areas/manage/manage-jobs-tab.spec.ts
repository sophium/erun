import type { Locator, Page, Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';

// The job store is the pod's, so these stub the RPC; the Go side is covered by environment_jobs_test.go.

function invokeMethod(request: Request): string {
  return (JSON.parse(request.postData() ?? '{}') as { method?: string }).method ?? '';
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const RUNNING_JOB = {
  id: 'gate-1',
  name: 'repo gate',
  state: 'running',
  kind: 'command',
  command: ['scripts/gate.sh', 'main'],
  exitCode: null,
  startedAtUnix: Math.floor(Date.now() / 1000) - 125,
};

const FAILED_JOB = {
  id: 'build-9',
  name: 'build',
  state: 'exited',
  kind: 'command',
  exitCode: 2,
  startedAtUnix: 1700000000,
  endedAtUnix: 1700000075,
};

// A job whose supervisor is gone without an outcome: neither success nor failure.
const UNKNOWN_JOB = {
  id: 'agent-3',
  name: 'agent run',
  state: 'unknown',
  kind: 'agent',
  agentTool: 'claude',
  exitCode: null,
  startedAtUnix: 1700000000,
  endedAtUnix: 1700003600,
};

// Terminal but not verdicts; their zero exit code must not render as success.
const ABANDONED_JOB = {
  id: 'gate-4',
  name: 'repo gate',
  state: 'abandoned',
  kind: 'command',
  command: ['scripts/gate.sh', 'main'],
  exitCode: 0,
  startedAtUnix: 1700000000,
  endedAtUnix: 1700000900,
};

const GATE_INCOMPLETE_JOB = {
  id: 'agent-7',
  name: 'automated fix',
  state: 'gate-incomplete',
  kind: 'agent',
  agentTool: 'claude',
  exitCode: 0,
  startedAtUnix: 1700000000,
  endedAtUnix: 1700001200,
};

// Real agent jobs carry the whole prompt as one argv entry.
const LONG_PROMPT = 'Investigate the flaky occupancy banner test and land a fix. '.repeat(90);

const AGENT_JOB_WITH_LONG_PROMPT = {
  id: 'agent-42',
  name: 'automated fix',
  state: 'exited',
  kind: 'agent',
  agentTool: 'claude',
  command: ['claude', '-p', LONG_PROMPT, '--output-format', 'stream-json', '--verbose'],
  exitCode: 0,
  startedAtUnix: 1700000000,
  endedAtUnix: 1700000600,
};

async function stubJobs(
  page: Page,
  jobs: unknown[],
  extra?: Record<string, unknown>,
  options?: { holdMs?: number },
): Promise<void> {
  // Anchored to the request so the delay is independent of dialog open time; only the first read is held.
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const method = invokeMethod(request);
    if (method === 'LoadEnvironmentJobs') {
      if (options?.holdMs) {
        if (holdUntil === 0) {
          holdUntil = Date.now() + options.holdMs;
        }
        const remaining = holdUntil - Date.now();
        if (remaining > 0) {
          await new Promise((resolve) => setTimeout(resolve, remaining));
        }
      }
      return fulfillJSON(route, jobs);
    }
    if (extra && method in extra) {
      return fulfillJSON(route, extra[method]);
    }
    await route.continue();
  });
}

// expect's 10s default is independent of the test's budget, so wait on the loaded state first.
async function convergeOnJobsTab(app: AppShell, loaded: Locator): Promise<void> {
  await app.manageDialog.jobsTabTrigger().click();
  await loaded.waitFor({ state: 'visible' });
}

test.describe('manage dialog jobs tab', () => {
  test('an empty store explains what the surface is for', async ({ app, page }) => {
    await stubJobs(page, []);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobsEmptyState());

    await expect(app.manageDialog.jobsEmptyState()).toContainText('No jobs yet');
    await expect(app.manageDialog.jobRows()).toHaveCount(0);

    await app.manageDialog.cancel();
  });

  // The empty state must not share a test id with the loading placeholder, or the wait settles early.
  test('an empty store is waited out, not read off the loading placeholder', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await stubJobs(page, [], undefined, { holdMs: 14_000 });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobsEmptyState());

    await expect(app.manageDialog.jobsEmptyState()).toContainText('No jobs yet');
    await expect(app.manageDialog.jobRows()).toHaveCount(0);

    await app.manageDialog.cancel();
  });

  test('each outcome reads distinctly, and a missing one is never a success', async ({
    app,
    page,
  }) => {
    await stubJobs(page, [RUNNING_JOB, FAILED_JOB, UNKNOWN_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    // The handler answers all rows at once, so the third row signals load.
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(2));

    await expect(app.manageDialog.jobRows()).toHaveCount(3);
    await expect(app.manageDialog.jobOutcome(0)).toContainText('Running');
    await expect(app.manageDialog.jobOutcome(1)).toContainText('Failed (exit 2)');
    await expect(app.manageDialog.jobOutcome(2)).toContainText('Outcome unknown');
    await expect(app.manageDialog.jobOutcome(2)).not.toContainText('Succeeded');

    // The running job's duration is relative to now, so only its shape is asserted.
    await expect(app.manageDialog.locator().getByText('Took 1m15s')).toBeVisible();
    await expect(app.manageDialog.locator().getByText(/^Running for \d+m\d*s?$/)).toBeVisible();

    await app.manageDialog.cancel();
  });

  test('an abandoned or incomplete job is never rendered as a success', async ({ app, page }) => {
    await stubJobs(page, [ABANDONED_JOB, GATE_INCOMPLETE_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(1));

    await expect(app.manageDialog.jobRows()).toHaveCount(2);

    await expect(app.manageDialog.jobOutcome(0)).toContainText('Abandoned (work still running)');
    await expect(app.manageDialog.jobOutcome(0)).not.toContainText('Succeeded');
    await expect(app.manageDialog.jobOutcome(0)).not.toContainText('Failed (exit 0)');
    await expect(app.manageDialog.jobOutcome(0)).toHaveClass(/\btext-destructive\b/);

    await expect(app.manageDialog.jobOutcome(1)).toContainText('Gate incomplete (no verdict)');
    await expect(app.manageDialog.jobOutcome(1)).not.toContainText('Succeeded');
    await expect(app.manageDialog.jobOutcome(1)).not.toContainText('Failed (exit 0)');
    await expect(app.manageDialog.jobOutcome(1)).toHaveClass(/\btext-amber-700\b/);

    // Colour never carries the outcome alone; the glyph does.
    await expect(app.manageDialog.locator().locator('.lucide-circle-slash')).toHaveCount(1);
    await expect(app.manageDialog.locator().locator('.lucide-hourglass')).toHaveCount(1);

    await app.manageDialog.cancel();
  });

  test('output is read on demand, and no output says so', async ({ app, page }) => {
    await stubJobs(page, [FAILED_JOB], {
      ReadEnvironmentJobOutput: {
        job: FAILED_JOB,
        offset: 0,
        nextOffset: 0,
        output: '',
        hasMore: false,
        complete: true,
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().first());

    await expect(app.manageDialog.jobOutput()).toHaveCount(0);
    await app.manageDialog.jobShowOutputButton('build').click();
    await app.manageDialog.jobOutputEmpty().waitFor({ state: 'visible' });
    await expect(app.manageDialog.jobOutputEmpty()).toContainText('produced no output');

    await app.manageDialog.cancel();
  });

  // erun-ui/AGENTS.md requires evidence the CSS actually engages.
  test('an extremely long job name truncates instead of overflowing the row', async ({
    app,
    page,
  }) => {
    const longName = 'extremely-long-job-name-'.repeat(20);
    await stubJobs(page, [{ ...RUNNING_JOB, name: longName }]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRowName(0));

    const name = app.manageDialog.jobRowName(0);
    await expect(name).toBeVisible();
    const { clientWidth, scrollWidth } = await name.evaluate((el) => ({
      clientWidth: el.clientWidth,
      scrollWidth: el.scrollWidth,
    }));
    expect(clientWidth).toBeGreaterThan(0);
    expect(scrollWidth).toBeGreaterThan(clientWidth);

    await app.manageDialog.cancel();
  });

  test('an agent job with a multi-kilobyte prompt does not flood the row', async ({
    app,
    page,
  }) => {
    expect(AGENT_JOB_WITH_LONG_PROMPT.command.join(' ').length).toBeGreaterThan(5000);

    await stubJobs(page, [AGENT_JOB_WITH_LONG_PROMPT]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(0));

    const row = app.manageDialog.jobRows().nth(0);
    await expect(row).toBeVisible();

    const rowText = await row.innerText();
    expect(rowText.length).toBeLessThan(500);
    expect(rowText).not.toContain('flaky occupancy banner');
    expect(rowText).toContain('Claude agent');

    const box = await boundingBoxOf(row, 'agent job row');
    expect(box.height).toBeLessThan(150);

    await app.manageDialog.cancel();
  });

  // Pin the darkened amber so a style pass cannot lighten it below WCAG AA.
  test('the unknown-outcome badge keeps its darkened, contrast-safe color', async ({
    app,
    page,
  }) => {
    await stubJobs(page, [UNKNOWN_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobOutcome(0));

    await expect(app.manageDialog.jobOutcome(0)).toHaveClass(/\btext-amber-700\b/);
    await expect(app.manageDialog.jobOutcome(0)).not.toHaveClass(/\btext-amber-600\b/);
  });

  // A stale port-forward may hide running jobs, so it must not read as empty.
  test('a stale port-forward is reported as unreachable, never as no jobs', async ({
    app,
    page,
  }) => {
    const staleMessage =
      'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: the port-forward for ' +
      `${SEED_TENANT}/${SEED_ENV_ALPHA} on 127.0.0.1:17999 is not carrying traffic ` +
      '(the local port is held but the edge never answers) — re-establishing it';
    await page.route('**/__erun_invoke', async (route, request) => {
      const method = invokeMethod(request);
      if (method === 'LoadEnvironmentJobs') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: staleMessage }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const unreachable = app.manageDialog.jobsUnreachable();
    await convergeOnJobsTab(app, unreachable);

    await expect(unreachable).toBeVisible();
    await expect(unreachable).toContainText('Cannot reach the environment runtime');
    await expect(app.manageDialog.jobsUnreachableReconnectButton()).toContainText('Reconnect…');
    await expect(app.manageDialog.jobsEmptyState()).toHaveCount(0);

    await app.manageDialog.cancel();
  });

  test('a refused read offers a Retry that re-issues the read', async ({ app, page }) => {
    let reads = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      if (invokeMethod(request) !== 'LoadEnvironmentJobs') {
        await route.continue();
        return;
      }
      reads += 1;
      if (reads === 1) {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: 'context deadline exceeded talking to the pod' }),
        });
      }
      return fulfillJSON(route, [RUNNING_JOB]);
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobsReadFailure());

    await expect(app.manageDialog.jobsReadFailure()).toContainText(
      'context deadline exceeded talking to the pod',
    );
    await expect(app.manageDialog.jobsEmptyState()).toHaveCount(0);
    await expect(app.manageDialog.jobsReadFailureRetry()).toBeEnabled();

    await app.manageDialog.jobsReadFailureRetry().click();

    await expect(app.manageDialog.jobsReadFailure()).toHaveCount(0);
    await app.manageDialog.jobRows().first().waitFor({ state: 'visible' });
    await expect(app.manageDialog.jobRows()).toHaveCount(1);
    expect(reads).toBeGreaterThan(1);

    await app.manageDialog.cancel();
  });

  test('a retry that fails again reports the fresh cause', async ({ app, page }) => {
    let reads = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      if (invokeMethod(request) !== 'LoadEnvironmentJobs') {
        await route.continue();
        return;
      }
      reads += 1;
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          error: reads === 1 ? 'context deadline exceeded talking to the pod' : 'bad gateway',
        }),
      });
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobsReadFailure());

    await expect(app.manageDialog.jobsReadFailure()).toContainText('context deadline exceeded');

    await app.manageDialog.jobsReadFailureRetry().click();

    // The old cause stays on screen until the new one arrives.
    await expect(app.manageDialog.jobsReadFailure()).toContainText('bad gateway', withTestBudget());
    await expect(app.manageDialog.jobsReadFailure()).not.toContainText('context deadline exceeded');

    await app.manageDialog.cancel();
  });

  test('a refused cancel is reported beside the job', async ({ app, page }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const method = invokeMethod(request);
      if (method === 'LoadEnvironmentJobs') {
        return fulfillJSON(route, [RUNNING_JOB]);
      }
      if (method === 'CancelEnvironmentJob') {
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'the job is no longer running' }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().first());

    await app.manageDialog.jobCancelButton('repo gate').click();
    const confirm = app.manageDialog.jobConfirmCancelButton('repo gate');
    await confirm.waitFor({ state: 'visible' });
    await confirm.click();

    await expect(app.manageDialog.locator().getByRole('alert')).toContainText(
      'the job is no longer running',
      withTestBudget(),
    );
    await expect(app.manageDialog.jobRows()).toHaveCount(1);

    await app.manageDialog.cancel();
  });

  // The hold exceeds expect's 10s default while staying inside the test's budget.
  test('a jobs read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await stubJobs(page, [RUNNING_JOB, FAILED_JOB, UNKNOWN_JOB], undefined, { holdMs: 12_000 });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(2));

    await expect(app.manageDialog.jobRows()).toHaveCount(3);
    await expect(app.manageDialog.jobOutcome(1)).toContainText('Failed (exit 2)');

    await app.manageDialog.cancel();
  });
});

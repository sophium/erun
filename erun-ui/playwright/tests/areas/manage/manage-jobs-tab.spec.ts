import type { Page, Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The desktop writes into the job store — Investigate starts a job there — and
// could not display the job it had just created. The job store itself is the
// pod's, so these drive the bindings over a stubbed RPC; the Go side is covered
// by erun-ui/environment_jobs_test.go.

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

// A job whose supervisor is gone without an outcome. Reporting it as success
// would be a lie and reporting it as failure would blame work that may have
// finished, so it has to read as neither.
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

// Every orchestrator-driven job is an agent job, and an agent job's argv
// always carries the whole prompt as one argument -- this is the shape a real
// `claude -p '<prompt>' --output-format stream-json --verbose` job takes.
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
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const method = invokeMethod(request);
    if (method === 'LoadEnvironmentJobs') {
      return fulfillJSON(route, jobs);
    }
    if (extra && method in extra) {
      return fulfillJSON(route, extra[method]);
    }
    await route.continue();
  });
}

test.describe('manage dialog jobs tab', () => {
  test('an empty store explains what the surface is for', async ({ app, page }) => {
    await stubJobs(page, []);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.jobsTabTrigger().click();

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
    await app.manageDialog.jobsTabTrigger().click();

    await expect(app.manageDialog.jobRows()).toHaveCount(3);
    await expect(app.manageDialog.jobOutcome(0)).toContainText('Running');
    // The exit code is named, not just "failed" — 2 is actionable, "failed" is not.
    await expect(app.manageDialog.jobOutcome(1)).toContainText('Failed (exit 2)');
    await expect(app.manageDialog.jobOutcome(2)).toContainText('Outcome unknown');
    await expect(app.manageDialog.jobOutcome(2)).not.toContainText('Succeeded');

    // The finished job's duration is derived from two fixed timestamps, so it is
    // asserted exactly. The running one is elapsed-from-now by definition, so
    // only its shape is asserted -- pinning a wall-clock value would make this
    // spec fail whenever the render lands a second later than the fixture.
    await expect(app.manageDialog.locator().getByText('Took 1m15s')).toBeVisible();
    await expect(app.manageDialog.locator().getByText(/^Running for \d+m\d*s?$/)).toBeVisible();

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
    await app.manageDialog.jobsTabTrigger().click();

    await expect(app.manageDialog.jobOutput()).toHaveCount(0);
    await app.manageDialog.jobShowOutputButton('build').click();
    // Distinct from "not read yet" and from an error.
    await expect(app.manageDialog.jobOutputEmpty()).toContainText('produced no output');

    await app.manageDialog.cancel();
  });

  // A row's header is a fixed-width flex line (name + outcome badge); an
  // unbounded name would push the badge off, or wrap and blow out row height.
  // erun-ui/AGENTS.md requires evidence the CSS actually engages, not just
  // that the element renders.
  test('an extremely long job name truncates instead of overflowing the row', async ({
    app,
    page,
  }) => {
    const longName = 'extremely-long-job-name-'.repeat(20);
    await stubJobs(page, [{ ...RUNNING_JOB, name: longName }]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.jobsTabTrigger().click();

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

  // An agent job's argv always carries the full prompt as one argument, so
  // rendering it raw would flood the row and push every other job below the
  // fold. The row must show an operator-readable summary instead, bounded to
  // one line, regardless of how long the underlying command is.
  test('an agent job with a multi-kilobyte prompt does not flood the row', async ({
    app,
    page,
  }) => {
    expect(AGENT_JOB_WITH_LONG_PROMPT.command.join(' ').length).toBeGreaterThan(5000);

    await stubJobs(page, [AGENT_JOB_WITH_LONG_PROMPT]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.jobsTabTrigger().click();

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

  // #4aecd83e darkened this badge's amber for WCAG AA contrast; pin the class
  // so a future style pass cannot quietly lighten it back.
  test('the unknown-outcome badge keeps its darkened, contrast-safe color', async ({
    app,
    page,
  }) => {
    await stubJobs(page, [UNKNOWN_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.jobsTabTrigger().click();

    await expect(app.manageDialog.jobOutcome(0)).toHaveClass(/\btext-amber-700\b/);
    await expect(app.manageDialog.jobOutcome(0)).not.toHaveClass(/\btext-amber-600\b/);
  });

  // The desktop's job store is host-local; a remote-agent/runtime env's jobs
  // run in its pod. A stale port-forward means a job may well be running
  // behind it right now, so this must read as "cannot tell", never silently
  // fall through to the same empty state a genuinely idle environment shows
  // (erun-ui/environment_jobs_test.go covers the Go-side branch this drives).
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
    await app.manageDialog.jobsTabTrigger().click();

    const unreachable = app.manageDialog.jobsUnreachable();
    await expect(unreachable).toBeVisible();
    await expect(unreachable).toContainText('Cannot reach the environment runtime');
    await expect(app.manageDialog.jobsUnreachableReconnectButton()).toContainText('Reconnect…');
    await expect(app.manageDialog.jobsEmptyState()).toHaveCount(0);

    await app.manageDialog.cancel();
  });

  // The failure path: a refused cancel must say so beside the control and leave
  // the job listed, rather than silently doing nothing.
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
    await app.manageDialog.jobsTabTrigger().click();

    await app.manageDialog.jobCancelButton('repo gate').click();
    // Cancelling work in flight takes a deliberate second press.
    await expect(app.manageDialog.jobConfirmCancelButton('repo gate')).toBeVisible();
    await app.manageDialog.jobConfirmCancelButton('repo gate').click();

    await expect(app.manageDialog.locator().getByRole('alert')).toContainText(
      'the job is no longer running',
    );
    await expect(app.manageDialog.jobRows()).toHaveCount(1);

    await app.manageDialog.cancel();
  });
});

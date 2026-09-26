import type { Locator, Page, Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';

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

// Two terminal states that are not verdicts, and whose recorded exit code
// cannot speak for them. An abandoned job left processes running in its own
// process group; a gate-incomplete one ended while a job it started had not
// reached a verdict. Both exit cleanly by definition, so both carry the zero
// that used to render them as green successes.
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
  options?: { holdMs?: number },
): Promise<void> {
  // holdMs makes the tab's own read land a fixed window after the request that
  // asked for it, so a panel that is merely slow to answer can be told apart
  // from one that never converges. Anchored to the request (not to the stub's
  // own registration) so the delay is the same however long the dialog took to
  // open, and only the first read is held -- a refetch is a separate step with
  // its own convergence. See the held-read case at the end of this file for why
  // it exists.
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

// convergeOnJobsTab settles the tab's own read before anything inside it is
// asserted on.
//
// Every assertion below reads a value the LoadEnvironmentJobs route handler
// owns, and `expect(...)` carries no timeout of its own: with no explicit one
// it resolves to expect.timeout (playwright.config.ts sets 10s on POSIX),
// which is independent of the 30s the test itself declared. A tab whose read is
// merely slow under a loaded builder therefore reds the step with two thirds of
// the test's budget unspent. `waitFor` is the idiom that defers to the test on
// its own (erun-ui/playwright/AGENTS.md, "No flaky tests"), so the tab is
// waited to its loaded state here and the assertions that follow then read
// already-rendered DOM rather than racing the read.
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

  test('each outcome reads distinctly, and a missing one is never a success', async ({
    app,
    page,
  }) => {
    await stubJobs(page, [RUNNING_JOB, FAILED_JOB, UNKNOWN_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    // The third row is the read's own completion signal: the handler answers
    // all three at once, so its appearance is what says the tab has loaded.
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(2));

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

  // Abandoned and gate-incomplete are terminal without being verdicts, and the
  // process that exited cleanly is not the work. Both carry a zero exit code,
  // which is exactly what the badge used to read as "Succeeded" -- for
  // abandoned, the state that most needs an operator to clean up after it.
  test('an abandoned or incomplete job is never rendered as a success', async ({ app, page }) => {
    await stubJobs(page, [ABANDONED_JOB, GATE_INCOMPLETE_JOB]);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(1));

    await expect(app.manageDialog.jobRows()).toHaveCount(2);

    await expect(app.manageDialog.jobOutcome(0)).toContainText('Abandoned (work still running)');
    await expect(app.manageDialog.jobOutcome(0)).not.toContainText('Succeeded');
    await expect(app.manageDialog.jobOutcome(0)).not.toContainText('Failed (exit 0)');
    // Abandoned is the one outcome that needs acting on, so it wears the
    // destructive styling rather than the amber "unresolved" one.
    await expect(app.manageDialog.jobOutcome(0)).toHaveClass(/\btext-destructive\b/);

    await expect(app.manageDialog.jobOutcome(1)).toContainText('Gate incomplete (no verdict)');
    await expect(app.manageDialog.jobOutcome(1)).not.toContainText('Succeeded');
    await expect(app.manageDialog.jobOutcome(1)).not.toContainText('Failed (exit 0)');
    await expect(app.manageDialog.jobOutcome(1)).toHaveClass(/\btext-amber-700\b/);

    // Colour never carries the outcome on its own: each badge's own glyph is
    // what tells the two apart, and both apart from the failed row's XCircle.
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
    // Distinct from "not read yet" and from an error. The empty-output line is
    // the ReadEnvironmentJobOutput round trip's own answer, so it is waited to
    // its rendered state rather than raced on expect's 10s clock.
    await app.manageDialog.jobOutputEmpty().waitFor({ state: 'visible' });
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

  // #4aecd83e darkened this badge's amber for WCAG AA contrast; pin the class
  // so a future style pass cannot quietly lighten it back.
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
    const unreachable = app.manageDialog.jobsUnreachable();
    await convergeOnJobsTab(app, unreachable);

    await expect(unreachable).toBeVisible();
    await expect(unreachable).toContainText('Cannot reach the environment runtime');
    await expect(app.manageDialog.jobsUnreachableReconnectButton()).toContainText('Reconnect…');
    await expect(app.manageDialog.jobsEmptyState()).toHaveCount(0);

    await app.manageDialog.cancel();
  });

  // A read that timed out named its cause and offered no way out: the operator
  // had to switch tabs and hope the remount re-fetched, while the unreachable
  // card one tab away already carried a Retry. The retry must re-issue the
  // read, not dismiss the alert -- so this asserts the alert is *replaced* by
  // the list the second read returned.
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

    // "Could not read" is not "No jobs": nothing is known here, so the empty
    // state must not appear behind it.
    await expect(app.manageDialog.jobsReadFailure()).toContainText(
      'context deadline exceeded talking to the pod',
    );
    await expect(app.manageDialog.jobsEmptyState()).toHaveCount(0);
    await expect(app.manageDialog.jobsReadFailureRetry()).toBeEnabled();

    await app.manageDialog.jobsReadFailureRetry().click();

    await expect(app.manageDialog.jobsReadFailure()).toHaveCount(0);
    // The retry must be seen to have re-issued the read, so wait on the row the
    // second read returns rather than on a bare count.
    await app.manageDialog.jobRows().first().waitFor({ state: 'visible' });
    await expect(app.manageDialog.jobRows()).toHaveCount(1);
    expect(reads).toBeGreaterThan(1);

    await app.manageDialog.cancel();
  });

  // The other half of "re-issues the read": a retry that fails again must show
  // the new failure rather than leave the operator on a stale cause they have
  // already read.
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

    // The fresh cause is the second read's own answer, so it waits on the
    // budget this test declared rather than on expect's 10s default: the old
    // cause is still on screen until the new one arrives, and a retry that is
    // merely slow must not read as a retry that reported the stale cause.
    await expect(app.manageDialog.jobsReadFailure()).toContainText('bad gateway', withTestBudget());
    await expect(app.manageDialog.jobsReadFailure()).not.toContainText('context deadline exceeded');

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
    await convergeOnJobsTab(app, app.manageDialog.jobRows().first());

    await app.manageDialog.jobCancelButton('repo gate').click();
    // Cancelling work in flight takes a deliberate second press.
    const confirm = app.manageDialog.jobConfirmCancelButton('repo gate');
    await confirm.waitFor({ state: 'visible' });
    await confirm.click();

    // The refusal is CancelEnvironmentJob's own answer, which arrives over a
    // round trip the route handler above owns -- so it carries this test's
    // clock rather than expect's 10s default.
    await expect(app.manageDialog.locator().getByRole('alert')).toContainText(
      'the job is no longer running',
      withTestBudget(),
    );
    await expect(app.manageDialog.jobRows()).toHaveCount(1);

    await app.manageDialog.cancel();
  });

  // The contention class this suite keeps paying for, constructed
  // deterministically rather than waited for on a loaded builder: the tab's own
  // read answering past Playwright's `expect.timeout` default (10s on POSIX --
  // playwright.config.ts) while sitting well inside the budget the test
  // declares.
  //
  // Pre-fix the first read of this tab's content is a bare `toHaveCount(3)`,
  // bounded by that 10s default, so it reds at "Timeout 10000ms exceeded" with
  // two thirds of the test's own budget unspent -- the step is merely slow, not
  // wrong. The delay is this spec's own, held on the route handler `stubJobs`
  // already installs, so the case behaves identically on an idle machine and a
  // throttled one.
  test('a jobs read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    // Past expect's 10s default, with the rest of the scenario well inside the
    // declared budget.
    await stubJobs(page, [RUNNING_JOB, FAILED_JOB, UNKNOWN_JOB], undefined, { holdMs: 12_000 });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnJobsTab(app, app.manageDialog.jobRows().nth(2));

    await expect(app.manageDialog.jobRows()).toHaveCount(3);
    await expect(app.manageDialog.jobOutcome(1)).toContainText('Failed (exit 2)');

    await app.manageDialog.cancel();
  });
});

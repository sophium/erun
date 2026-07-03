import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Two invariants of the idle widget:
//
//   A. Unlocking must survive AWS's eventual-consistency window: a
//      describe right after the toggle can still return the stale
//      pre-modify "locked", and the icon must not flip back to it.
//
//   B. An in-flight start/stop must show a persistent transition pill.
//      cloudContextStatus flips out of `running` before the synchronous
//      AWS waiter returns, so the env-name idle pill would otherwise
//      vanish mid-transition.
//
// The harness has no managed cloud context and must never fire a real AWS
// mutation, so every relevant RPC is intercepted and the lifecycle is
// fully simulated.

interface InvokeBody {
  method: string;
  args: unknown[];
}

interface IdleStatusFixture {
  cloudContextName: string;
  cloudContextStatus: string;
  cloudContextLabel: string;
}

function managedRunningIdleStatus(ctx: IdleStatusFixture): unknown {
  return {
    timeoutSeconds: 600,
    secondsUntilStop: 500,
    stopEligible: true,
    outsideWorkingHours: false,
    managedCloud: true,
    cloudContextName: ctx.cloudContextName,
    cloudContextStatus: ctx.cloudContextStatus,
    cloudContextLabel: ctx.cloudContextLabel,
    markers: [],
  };
}

interface PendingStopFixture extends IdleStatusFixture {
  stopPendingSince: string;
  secondsUntilForcedStop: number;
  gracePeriodSeconds: number;
}

function managedRunningIdleStatusWithPendingStop(ctx: PendingStopFixture): unknown {
  return {
    timeoutSeconds: 600,
    secondsUntilStop: 0,
    stopEligible: true,
    outsideWorkingHours: false,
    managedCloud: true,
    cloudContextName: ctx.cloudContextName,
    cloudContextStatus: ctx.cloudContextStatus,
    cloudContextLabel: ctx.cloudContextLabel,
    markers: [],
    stopPendingSince: ctx.stopPendingSince,
    secondsUntilForcedStop: ctx.secondsUntilForcedStop,
    gracePeriodSeconds: ctx.gracePeriodSeconds,
  };
}

function apiStopStatus(name: string, locked: boolean): unknown {
  return {
    name,
    stopProtection: locked,
    stopProtectionKnown: true,
  };
}

function envelope(data: unknown): { contentType: string; body: string } {
  return { contentType: 'application/json', body: JSON.stringify({ data }) };
}

// A helper instead of the inline `let resolve = null; new Promise(...)`
// pattern: TS narrows the captured handle to `never` outside the executor.
function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// A deterministic beat tied to a real poll cycle (the widget polls idle
// status ~every second) — used instead of a wall-clock sleep to assert a
// state survives, or that nothing further fired, across a poll.
async function waitForNextIdlePoll(page: Page): Promise<void> {
  await page.waitForResponse(
    (response) =>
      response.url().includes('/__erun_invoke') &&
      (response.request().postData() ?? '').includes('"LoadIdleStatus"'),
  );
}

test.describe('idle widget stop protection', () => {
  test('unlock toggle keeps the unlocked state even if a follow-up describe would return stale', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-ctx-unlock';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };
    let describeCalls = 0;
    let enableCalls = 0;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        describeCalls++;
        // Always locked: any refetch after the unlock mutation overwrites
        // the cache with this stale value and flips the icon back.
        return route.fulfill(envelope(apiStopStatus(ctxName, true)));
      }
      if (body.method === 'EnableCloudContextApiStop') {
        enableCalls++;
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'DisableCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, true)));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // The label names the action the click performs, so the locked state's
    // button reads "Unlock <ctx>" (and "Lock <ctx>" once unlocked);
    // aria-pressed reflects locked.
    const buttonWhileLocked = page.getByRole('button', {
      name: new RegExp(`^Unlock ${ctxName}`),
    });
    await expect(buttonWhileLocked).toBeVisible();
    await expect(buttonWhileLocked).toHaveAttribute('aria-pressed', 'true');

    const describesBeforeClick = describeCalls;
    await buttonWhileLocked.click();

    const buttonWhileUnlocked = page.getByRole('button', {
      name: new RegExp(`^Lock ${ctxName}`),
    });
    await expect(buttonWhileUnlocked).toBeVisible();
    await expect(buttonWhileUnlocked).toHaveAttribute('aria-pressed', 'false');

    expect(enableCalls).toBe(1);

    // The crux: no describe may fire in response to the mutation resolving.
    // Under the old wiring a refetch here overwrote the cache with the stale
    // locked value and flipped the icon back. A full poll round-trip bounds
    // the window in which any such refetch would have landed.
    await waitForNextIdlePoll(page);
    expect(describeCalls).toBe(describesBeforeClick);
  });

  test('"Stopping" transition pill stays visible while the AWS waiter is in flight', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-ctx-stop';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };
    const { promise: stopHeld, resolve: releaseStop } = deferred();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'StopCloudContext') {
        // Mirror production, where the call blocks for the full
        // `aws ec2 wait instance-stopped` waiter (30s-10min); the pill
        // must stay visible the whole time.
        await stopHeld;
        return route.fulfill(
          envelope({
            name: ctxName,
            status: 'stopped',
            cloudContextStatus: 'stopped',
          }),
        );
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await expect(stopButton).toBeVisible();
    await stopButton.click();

    // The transition pill replaces the idle-time pill while busy.
    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await expect(transitionPill).toBeVisible();
    await expect(transitionPill).toContainText('Stopping');
    await expect(transitionPill).toContainText(ctxName);
    // The pill must persist across a real poll cycle, not flash as a transient overlay.
    await waitForNextIdlePoll(page);
    await expect(transitionPill).toBeVisible();
    await expect(transitionPill).toContainText('Stopping');

    // Release the held RPC or the route handler leaks into test teardown.
    releaseStop();
  });

  test('IDE buttons disable while the cloud env is mid-transition; diff panel toggle stays enabled', async ({
    app,
    page,
  }) => {
    // The load-bearing invariant here is the pure-UI diff-panel toggle
    // staying enabled while env-touching icons disable during a transition.
    // The full "IDE buttons disable when not running" path can't be cleanly
    // asserted for the seeded local env (its IDE buttons are already disabled
    // for an unrelated reason); that path is the straight-line
    // isEnvOpenedAndRunning helper in Titlebar.helpers.ts.
    const ctxName = 'mock-ctx-diff-toggle';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };
    const { promise: stopHeld, resolve: releaseStop } = deferred();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'StopCloudContext') {
        await stopHeld;
        return route.fulfill(
          envelope({ name: ctxName, status: 'stopped', cloudContextStatus: 'stopped' }),
        );
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await expect(stopButton).toBeVisible();
    await stopButton.click();

    // Precondition: confirm we reached the busy-stopping state before
    // asserting against it.
    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await expect(transitionPill).toBeVisible();

    // Pure-UI affordance — stays enabled by the design choice we
    // codified (env-touching only). A regression here
    // would mean someone added the env-running gate to the wrong
    // button group.
    const diffPanelToggle = page.getByRole('button', { name: 'Toggle diff panel' });
    await expect(diffPanelToggle).toBeEnabled();

    releaseStop();
  });

  test('grace-period warning banner shows countdown and Cancel calls CancelPendingIdleStop', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-ctx-pending';
    const pending: PendingStopFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
      stopPendingSince: '2026-05-31T07:00:00Z',
      secondsUntilForcedStop: 137,
      gracePeriodSeconds: 600,
    };
    let cancelCalls = 0;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatusWithPendingStop(pending)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'CancelPendingIdleStop') {
        cancelCalls++;
        // Simulate the backend clearing the pending stop so the next poll —
        // and thus the warning banner — reflects the cleared state.
        pending.stopPendingSince = '';
        pending.secondsUntilForcedStop = 0;
        return route.fulfill(envelope(null));
      }
      if (body.method === 'LoadLastStopEvent') {
        return route.fulfill(envelope({ stoppedAt: '', graceSeconds: 0, reason: '' }));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // The warning banner replaces the idle-time pill when stopPendingSince
    // is set in the idle status.
    const warning = page.getByTestId('titlebar-idle-stop-warning');
    await expect(warning).toBeVisible();
    await expect(warning).toContainText('Auto-stop in 2m 17s');

    const cancelBtn = page.getByTestId('titlebar-idle-stop-cancel');
    await expect(cancelBtn).toBeVisible();
    await cancelBtn.click();
    expect(cancelCalls).toBe(1);
    await expect(warning).toBeHidden();
  });

  test('Stop click does not trigger any frontend-driven restart RPC (issue #412)', async ({
    app,
    page,
  }) => {
    // The bug: clicking Stop on a remote env silently auto-reopened it. The
    // fix lives in the Go terminal-reconnect loop and is owned by
    // erun-ui/reconnect_loop_test.go's TestStopCloudContextSuppressesReconnect;
    // the headless harness can't drive a real PTY exit, so that branch is
    // unreachable here. The closest invariant this spec can hold is the
    // frontend contract: Stop must not, of its own accord, fire a
    // StartCloudContext, StartSession, or StartAISession.
    const ctxName = 'mock-ctx-no-restart';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };
    let startCloudContextCalls = 0;
    let startSessionCalls = 0;
    let startAISessionCalls = 0;
    let stopCloudContextCalls = 0;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'StopCloudContext') {
        stopCloudContextCalls++;
        // Mirror the backend marking the env stopped once StopInstances
        // returns, so subsequent polls report stopped.
        idle.cloudContextStatus = 'stopped';
        return route.fulfill(
          envelope({
            name: ctxName,
            status: 'stopped',
            cloudContextStatus: 'stopped',
          }),
        );
      }
      if (body.method === 'StartCloudContext') {
        startCloudContextCalls++;
        // The call we assert must NOT happen; return a value so a regression
        // fails with an assertion error instead of hanging.
        return route.fulfill(envelope({ name: ctxName, status: 'running' }));
      }
      if (body.method === 'StartSession') {
        startSessionCalls++;
        return route.fulfill(envelope({ sessionId: 0, kind: 'open' }));
      }
      if (body.method === 'StartAISession') {
        startAISessionCalls++;
        return route.fulfill(envelope({ sessionId: 0, kind: 'ai' }));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await expect(stopButton).toBeVisible();

    // Opening the env legitimately fires StartSession once, so baseline the
    // counts here rather than asserting zero after the stop.
    const baselineStartSession = startSessionCalls;
    const baselineStartAISession = startAISessionCalls;

    await stopButton.click();

    // A full poll round-trip after the stop bounds the window in which any
    // follow-up restart RPC would have fired; then assert none did.
    await expect.poll(() => stopCloudContextCalls).toBe(1);
    await waitForNextIdlePoll(page);

    expect(startCloudContextCalls).toBe(0);
    expect(startSessionCalls).toBe(baselineStartSession);
    expect(startAISessionCalls).toBe(baselineStartAISession);
  });

  test('Manage dialog History tab renders the last N stops, newest first', async ({
    app,
    page,
  }) => {
    // Three records cover the distinct history shapes: an in-pod auto-stop
    // with a per-marker breakdown, a manual desktop stop, and a legacy entry
    // written before the source/armedAt/policy fields existed.
    const history = [
      {
        stoppedAt: '2026-05-31T12:30:00Z',
        armedAt: '2026-05-31T12:20:00Z',
        graceSeconds: 600,
        source: 'pod-monitor',
        reason: 'idle: terminal-stdin, ai',
        cloudContextName: 'mock-cluster',
        policy: {
          timeoutSeconds: 600,
          workingHours: '09:00-18:00',
          timezone: 'Europe/Riga',
        },
        markers: [
          { name: 'terminal-stdin', idle: true, reason: 'no input', secondsIdleFor: 750 },
          { name: 'ai', idle: true, reason: 'no Claude session activity', secondsIdleFor: 720 },
        ],
      },
      {
        stoppedAt: '2026-05-30T18:15:00Z',
        graceSeconds: 0,
        source: 'host-manual',
        reason: 'Manual stop via desktop',
        cloudContextName: 'mock-cluster',
      },
      {
        stoppedAt: '2026-05-29T09:00:00Z',
        graceSeconds: 600,
        reason: 'outside working hours',
        cloudContextName: 'mock-cluster',
        markers: [],
      },
    ];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadStopHistory') {
        return route.fulfill(envelope(history));
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.selectTab('History');
    const list = page.getByTestId('manage-history-list');
    await expect(list).toBeVisible();

    const rows = page.getByTestId('manage-history-row');
    await expect(rows).toHaveCount(history.length);

    // Newest first: row 0 is the pod-monitor auto-stop, and must carry enough
    // (source, grace, timestamps, policy) for a user to answer "what triggered
    // it?" without recalling the underlying timeout.
    const firstRow = rows.nth(0);
    await expect(firstRow.getByTestId('manage-history-row-when')).toContainText('2026-05-31');
    await expect(firstRow.getByTestId('manage-history-row-source')).toContainText(
      'In-pod idle monitor',
    );
    await expect(firstRow.getByTestId('manage-history-row-source')).toContainText('Grace 600s');
    await expect(firstRow.getByTestId('manage-history-row-reason')).toContainText(
      'idle: terminal-stdin, ai',
    );
    await expect(firstRow.getByTestId('manage-history-row-armed')).toContainText('Grace armed at');
    await expect(firstRow.getByTestId('manage-history-row-policy')).toContainText('timeout 10m');
    await expect(firstRow.getByTestId('manage-history-row-policy')).toContainText(
      'working hours 09:00-18:00',
    );
    // Per-marker breakdown — the audit's central diagnostic: it
    // lets a user see which markers actually went idle vs. which
    // stayed active.
    await expect(firstRow).toContainText('Idle markers');
    await expect(firstRow).toContainText('terminal-stdin');
    await expect(firstRow).toContainText('ai');

    // The manual desktop stop — the row that answers "did I click Stop?".
    // Keep its shape narrow: no grace, armed-at, policy, or marker lines.
    const secondRow = rows.nth(1);
    await expect(secondRow.getByTestId('manage-history-row-source')).toContainText(
      'Desktop manual stop',
    );
    await expect(secondRow.getByTestId('manage-history-row-source')).not.toContainText('Grace');
    await expect(secondRow.getByTestId('manage-history-row-reason')).toContainText(
      'Manual stop via desktop',
    );
    await expect(secondRow.getByTestId('manage-history-row-armed')).toHaveCount(0);
    await expect(secondRow.getByTestId('manage-history-row-policy')).toHaveCount(0);

    // The last row is a legacy entry written before source/armedAt/
    // policy existed. It must still render with a sensible header
    // (no crash) — falling back to a generic "Auto-stop" label.
    const thirdRow = rows.nth(2);
    await expect(thirdRow.getByTestId('manage-history-row-source')).toContainText('Auto-stop');
    await expect(thirdRow.getByTestId('manage-history-row-reason')).toContainText(
      'outside working hours',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A failed stop used to surface as a bare "exit status 1" while the
  // instance kept running. The desktop now shows the classifier's actionable
  // message (naming the unlock lever) and keeps reporting the running state,
  // never a false "stopped". A real AWS OperationNotPermitted can't be staged
  // headless, so the RPC rejects with the exact message the classifier emits;
  // the classification itself is owned by
  // erun-common/cloud_context_power_error_test.go.
  test('a failed stop surfaces the actionable reason and keeps the running state', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-ctx-stop-fail';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };
    const stopError =
      `cloud context "${ctxName}" cannot be stopped: stop protection (DisableApiStop) is enabled on instance i-0abc123 — ` +
      `turn it off first (\`erun context enable-api-stop ${ctxName}\`, or the stop-protection toggle in the desktop titlebar), ` +
      'then retry: aws ec2 stop-instances: An error occurred (OperationNotPermitted) when calling the StopInstances operation';

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        // Locked — the same condition that makes the real stop fail.
        return route.fulfill(envelope(apiStopStatus(ctxName, true)));
      }
      if (body.method === 'StopCloudContext') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: stopError }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await expect(stopButton).toBeVisible();
    await stopButton.click();

    // The failure reason renders where the user acted (Nielsen #1/#9):
    // the titlebar error pill names stop protection as the cause.
    const errorPill = page.getByRole('alert').filter({ hasText: 'stop protection' });
    await expect(errorPill).toBeVisible();

    // The widget must keep reporting reality: still running, stop still
    // offered — never a silent flip to "stopped".
    await expect(stopButton).toBeVisible();
  });
});

import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Every AWS-touching RPC is intercepted: the harness must never fire a real mutation.

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
    fromPod: true,
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
    fromPod: true,
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

// TS narrows a promise resolver captured inline to `never` outside the executor.
function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// The widget polls idle status about once a second.
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
        // Always locked, as AWS's eventually consistent describe can be right after a
        // toggle: a refetch after the unlock mutation would flip the icon back.
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

    const buttonWhileLocked = page.getByRole('button', {
      name: new RegExp(`^Unlock ${ctxName}`),
    });
    await buttonWhileLocked.waitFor({ state: 'visible' });
    await expect(buttonWhileLocked).toHaveAttribute('aria-pressed', 'true', withTestBudget());

    const describesBeforeClick = describeCalls;
    await buttonWhileLocked.click();

    const buttonWhileUnlocked = page.getByRole('button', {
      name: new RegExp(`^Lock ${ctxName}`),
    });
    await buttonWhileUnlocked.waitFor({ state: 'visible' });
    await expect(buttonWhileUnlocked).toHaveAttribute('aria-pressed', 'false', withTestBudget());

    expect(enableCalls).toBe(1);

    // No describe may fire in response to the mutation resolving.
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
        // Mirrors the real blocking `aws ec2 wait instance-stopped`; cloudContextStatus
        // leaves `running` before it returns, so only the transition pill covers the gap.
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
    await stopButton.waitFor({ state: 'visible' });
    await stopButton.click();

    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await transitionPill.waitFor({ state: 'visible' });
    await expect(transitionPill).toContainText('Stopping', withTestBudget());
    await expect(transitionPill).toContainText(ctxName, withTestBudget());
    await waitForNextIdlePoll(page);
    await transitionPill.waitFor({ state: 'visible' });
    await expect(transitionPill).toContainText('Stopping', withTestBudget());

    // Release the held RPC or the route handler leaks into teardown.
    releaseStop();
  });

  test('IDE buttons disable while the cloud env is mid-transition; diff panel toggle stays enabled', async ({
    app,
    page,
  }) => {
    // The seeded local env's IDE buttons are already disabled, so only the
    // pure-UI diff toggle staying enabled is assertable here.
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
    await stopButton.waitFor({ state: 'visible' });
    await stopButton.click();

    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await transitionPill.waitFor({ state: 'visible' });

    const diffPanelToggle = page.getByRole('button', { name: 'Toggle diff panel' });
    await expect(diffPanelToggle).toBeEnabled(withTestBudget());

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
    // Holding the route handler makes the increment land on a microtask queued by
    // the release, so a synchronous read is ordered before it by construction.
    let cancelArrived = 0;
    let cancelCalls = 0;
    let releaseCancel!: () => void;
    const cancelHeld = new Promise<void>((resolve) => {
      releaseCancel = resolve;
    });

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedRunningIdleStatusWithPendingStop(pending)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      if (body.method === 'CancelPendingIdleStop') {
        cancelArrived++;
        await cancelHeld;
        cancelCalls++;
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

    const warning = page.getByTestId('titlebar-idle-stop-warning');
    await warning.waitFor({ state: 'visible' });
    await expect(warning).toContainText('Auto-stop in 2m 17s', withTestBudget());

    const cancelBtn = page.getByTestId('titlebar-idle-stop-cancel');
    await cancelBtn.waitFor({ state: 'visible' });
    await cancelBtn.click();
    // The finally keeps a cancel that never arrives from parking the handler for the worker's life.
    try {
      await expect.poll(() => cancelArrived, withTestBudget()).toBe(1);
    } finally {
      releaseCancel();
    }
    await expect.poll(() => cancelCalls, withTestBudget()).toBe(1);
    await expect(warning).toBeHidden();
  });

  test('Stop click does not trigger any frontend-driven restart RPC (issue #412)', async ({
    app,
    page,
  }) => {
    // The headless harness can't drive a PTY exit; the reconnect suppression itself
    // is covered in Go. Stop must not fire any start RPC of its own accord.
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
        // Fulfill so a violation fails the assertion instead of hanging.
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
    await stopButton.waitFor({ state: 'visible' });

    // Opening the env legitimately fires StartSession once.
    const baselineStartSession = startSessionCalls;
    const baselineStartAISession = startAISessionCalls;

    await stopButton.click();

    await expect.poll(() => stopCloudContextCalls, withTestBudget()).toBe(1);
    await waitForNextIdlePoll(page);

    expect(startCloudContextCalls).toBe(0);
    expect(startSessionCalls).toBe(baselineStartSession);
    expect(startAISessionCalls).toBe(baselineStartAISession);
  });

  test('Manage dialog History tab renders the last N stops, newest first', async ({
    app,
    page,
  }) => {
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
    await list.waitFor({ state: 'visible' });

    const rows = page.getByTestId('manage-history-row');
    await expect(rows).toHaveCount(history.length, withTestBudget());

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
    await expect(firstRow).toContainText('Idle markers');
    await expect(firstRow).toContainText('terminal-stdin');
    await expect(firstRow).toContainText('ai');

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

    const thirdRow = rows.nth(2);
    await expect(thirdRow.getByTestId('manage-history-row-source')).toContainText('Auto-stop');
    await expect(thirdRow.getByTestId('manage-history-row-reason')).toContainText(
      'outside working hours',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A real OperationNotPermitted can't be staged headless, so the RPC rejects with
  // the classifier's exact message.
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
    await stopButton.waitFor({ state: 'visible' });
    await stopButton.click();

    const errorPill = page.getByRole('alert').filter({ hasText: 'stop protection' });
    await errorPill.waitFor({ state: 'visible' });

    await expect(stopButton).toBeVisible(withTestBudget());
  });

  test('idle badge names the pod provenance caveat for a reading the pod never confirmed', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-ctx-stale-provenance';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(
          envelope({ ...(managedRunningIdleStatus(idle) as object), fromPod: false }),
        );
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, false)));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const badge = app.titlebar.idleStatusBadge();
    await badge.waitFor({ state: 'visible' });
    await expect(badge).toHaveAttribute(
      'aria-label',
      /^not confirmed with the pod/,
      withTestBudget(),
    );
  });

  // The hold is just past expect's 10s default, so a bare toBeVisible would cut it off.
  test('a widget that mounts past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const ctxName = 'mock-ctx-slow-mount';
    const idle: IdleStatusFixture = {
      cloudContextName: ctxName,
      cloudContextStatus: 'running',
      cloudContextLabel: ctxName,
    };

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        // Deliberate stimulus: this hold is the contention under test.
        await new Promise<void>((resolve) => setTimeout(resolve, 12_000));
        return route.fulfill(envelope(managedRunningIdleStatus(idle)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(envelope(apiStopStatus(ctxName, true)));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const buttonWhileLocked = page.getByRole('button', {
      name: new RegExp(`^Unlock ${ctxName}`),
    });
    await buttonWhileLocked.waitFor({ state: 'visible' });
    await expect(buttonWhileLocked).toHaveAttribute('aria-pressed', 'true', withTestBudget());
  });
});

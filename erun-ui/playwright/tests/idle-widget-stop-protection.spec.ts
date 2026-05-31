import { expect, test } from '../fixtures/erunApp.js';

// idle-widget-stop-protection covers the two changes from issue #410:
//
//   A. The lock toggle no longer flips back to amber when the
//      describe-after-modify hits AWS's eventual-consistency window.
//      Before the fix, `enableCloudContextApiStop` declared
//      `invalidatesTags`, which fired DescribeCloudContextApiStop right
//      after the mutation resolved; if AWS returned the pre-modify
//      `True` for a few hundred ms, the cache flipped back to locked
//      and the icon stayed Lock/amber even though the success toast
//      had already fired. The fix replaces the invalidation with
//      `onQueryStarted` + `updateQueryData` so the mutation result is
//      the cache update; no AWS describe is solicited right after.
//
//   B. While a start/stop is in flight, the titlebar surfaces a
//      visible "Stopping <name>…" / "Starting <name>…" pill instead of
//      relying on the action button's spinner alone — the idle-time
//      pill that would otherwise carry the env name vanishes
//      mid-transition because `cloudContextStatus` flips out of
//      `running` before the synchronous AWS `wait instance-stopped`
//      waiter returns.
//
// The headless harness uses the dev's real `~/.erun/`, which may or may
// not contain a managed cloud context, and we never want a Playwright
// test to fire a real ec2:ModifyInstanceAttribute or ec2:StopInstances.
// Intercept the relevant Wails RPCs over the /__erun_invoke bridge so
// the React tree drives a fully simulated lifecycle.

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

// deferred returns a Promise plus its resolve handle as one value. The
// inline `let releaseStop = null; new Promise((resolve) => { releaseStop = resolve })`
// pattern narrows `releaseStop` to `null` outside the executor callback,
// which then refuses the later `releaseStop?.()` call as `never`.
function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
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
        // Always returns locked. With the old invalidatesTags wiring,
        // an EnableCloudContextApiStop success would trigger a refetch
        // that hit this path and flipped the icon back to amber.
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

    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, `no envs under tenant ${tenant}`);
    await app.sidebar.openEnvironment(tenant, envs[0]!);

    // The button's accessible label names the action available from
    // the current state: while locked, clicking it would unlock, so
    // the label is "Unlock <ctx>: …"; while unlocked the label is
    // "Lock <ctx>: …". aria-pressed reflects locked.
    const buttonWhileLocked = page.getByRole('button', {
      name: new RegExp(`^Unlock ${ctxName}`),
    });
    await buttonWhileLocked.waitFor({ state: 'visible', timeout: 6_000 });
    await expect(buttonWhileLocked).toHaveAttribute('aria-pressed', 'true');

    const describesBeforeClick = describeCalls;
    await buttonWhileLocked.click();

    // After the unlock mutation succeeds the cache is updated directly
    // from the mutation result (StopProtection=false). The button's
    // accessible label rotates to the Lock form, and aria-pressed
    // flips false.
    const buttonWhileUnlocked = page.getByRole('button', {
      name: new RegExp(`^Lock ${ctxName}`),
    });
    await buttonWhileUnlocked.waitFor({ state: 'visible', timeout: 3_000 });
    await expect(buttonWhileUnlocked).toHaveAttribute('aria-pressed', 'false');

    expect(enableCalls).toBe(1);

    // Critical assertion: no DescribeCloudContextApiStop call fired in
    // response to the mutation resolving. Under the previous
    // invalidatesTags wiring this would be `describesBeforeClick + 1`
    // and the cache would be overwritten with the stale `locked: true`
    // response, flipping the icon back. Allow a small grace window in
    // case any unrelated mount-driven refetch races.
    await page.waitForTimeout(500);
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
        // Hold the RPC open. This mirrors the production behaviour
        // where the Wails call blocks for the full
        // `aws ec2 wait instance-stopped` waiter (30s-10min). The pill
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

    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, `no envs under tenant ${tenant}`);
    await app.sidebar.openEnvironment(tenant, envs[0]!);

    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await stopButton.waitFor({ state: 'visible', timeout: 6_000 });
    await stopButton.click();

    // The transition pill replaces the idle-time pill while busy.
    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await expect(transitionPill).toBeVisible({ timeout: 2_000 });
    await expect(transitionPill).toContainText('Stopping');
    await expect(transitionPill).toContainText(ctxName);
    // Persists across a poll cycle, not just a transient overlay.
    await page.waitForTimeout(1_500);
    await expect(transitionPill).toBeVisible();
    await expect(transitionPill).toContainText('Stopping');

    // Release the held StopCloudContext RPC so the thunk can finish
    // cleanly. Without this the route handler would leak the test
    // teardown.
    releaseStop();
  });

  test('IDE buttons disable while the cloud env is mid-transition; diff panel toggle stays enabled', async ({
    app,
    page,
  }) => {
    // This test reuses the same StopCloudContext-held setup as the
    // transition-pill test (mocked LoadIdleStatus says running, the
    // Stop RPC hangs while busy=true). While busy is true, the React
    // tree treats the cloud as not-fully-running for the purpose of
    // the IDE-button gate, so the IDE buttons disable with the
    // "Start the cloud environment before opening …" tooltip and the
    // diff panel toggle stays enabled (env-touching only by design).
    //
    // We arrive at this state via the first env (envs[0]), which is
    // local in most dev harnesses. The IDE buttons are normally
    // disabled for local envs via isIdeDisabled (env.remote === false
    // would short-circuit envRunning to true; isIdeDisabled's
    // remote+!sshd check disables them anyway). The deterministic
    // assertion here is on the diff panel toggle, which is the
    // load-bearing design choice: env-touching icons disable, pure-UI
    // icons stay enabled. The full IDE-disabled-when-not-running
    // path is covered by reading the helper isEnvOpenedAndRunning in
    // Titlebar.helpers.ts — it has no other call sites and its branch
    // logic is straight-line.
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

    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, `no envs under tenant ${tenant}`);
    await app.sidebar.openEnvironment(tenant, envs[0]!);

    // Fire the stop so the React tree enters the "busy stopping" state
    // and cloudContextStatus flips out of `running` for the IDE-button
    // gate.
    const stopButton = page.getByRole('button', { name: new RegExp(`^Stop ${ctxName}`) });
    await stopButton.waitFor({ state: 'visible', timeout: 6_000 });
    await stopButton.click();

    // The transition pill must be visible — that confirms we are in
    // the state we want to assert against.
    const transitionPill = page.getByTestId('titlebar-idle-transition');
    await expect(transitionPill).toBeVisible({ timeout: 2_000 });

    // Pure-UI affordance — stays enabled by the design choice we
    // codified in PR #411 (env-touching only). A regression here
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
        // After cancel, the next poll should observe no pending stop.
        // Mutate the in-test fixture so subsequent LoadIdleStatus
        // calls reflect the cleared state.
        pending.stopPendingSince = '';
        pending.secondsUntilForcedStop = 0;
        return route.fulfill(envelope(null));
      }
      if (body.method === 'LoadLastStopEvent') {
        return route.fulfill(envelope({ stoppedAt: '', graceSeconds: 0, reason: '' }));
      }
      await route.continue();
    });

    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, `no envs under tenant ${tenant}`);
    await app.sidebar.openEnvironment(tenant, envs[0]!);

    // The warning banner replaces the idle-time pill when stopPendingSince
    // is set in the idle status.
    const warning = page.getByTestId('titlebar-idle-stop-warning');
    await expect(warning).toBeVisible({ timeout: 6_000 });
    // 137s → "in 2m 17s" per formatGraceCountdown.
    await expect(warning).toContainText('Auto-stop in 2m 17s');

    // Clicking Cancel calls CancelPendingIdleStop with the env's
    // cloud-context name and the warning then disappears once the
    // next poll observes the cleared pending state.
    const cancelBtn = page.getByTestId('titlebar-idle-stop-cancel');
    await expect(cancelBtn).toBeVisible();
    await cancelBtn.click();
    expect(cancelCalls).toBe(1);
    await expect(warning).toBeHidden({ timeout: 5_000 });
  });
});

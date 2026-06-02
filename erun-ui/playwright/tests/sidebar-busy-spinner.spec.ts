import { expect, test } from '../fixtures/erunApp.js';

// sidebar-busy-spinner covers the running-command spinner that appears
// next to an env name when an activity-queue entry for that env is in
// 'running' state (deploy / init / build / release / push). A *deploy*
// entry cannot be staged from the headless harness (it needs the helm
// poller + cluster state per playwright/AGENTS.md), so the first two
// tests lock the negative invariant: a quiet env carries no spinner, and
// navigating away does not strand one.
//
// build / release / push entries, by contrast, are pure trace-driven
// entries keyed off the session selection (no cluster dependency), so the
// activity:state Wails event can stage one directly — the same staging
// ux-tooltip-rules.spec.ts uses. The third test drives the positive flow
// end to end: a running entry lights a labelled spinner on the env row,
// and finishing it clears the spinner. This is the observable contract
// that #428 wired up (CLI emits ==> Building/Releasing/Pushing, the
// desktop registers a running entry, deriveEnvironmentRow renders the
// spinner + per-command aria-label).

test.describe('sidebar busy spinner', () => {
  test('quiet env rows show no spinner', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    // The BusyRowSpinner mounts with role="status" and a per-env
    // accessible label. An idle env should expose no such role on its
    // row. We restrict the query to the sidebar to avoid colliding with
    // unrelated role="status" surfaces (e.g., terminal banners).
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0);
  });

  test('navigating away clears any in-flight spinner', async ({ app, page }) => {
    // openSelection's previous incarnation kept its markEnvOpening flag
    // until the underlying StartSession resolved. On a cold EC2 open
    // that could take ~60s, so a user who clicked env A and then env B
    // mid-flight ended up with a stale spinner on A *and* a stale
    // "Opening A..." status banner overwriting the env they were
    // actually looking at. The fix in this commit dispatches
    // resetEnvOpening at the top of each click and gates the post-
    // StartSession promotion behind isCurrentSelection. Lock the
    // observable settle state: after clicking two different envs back
    // to back, the sidebar must surface at most one spinner, and any
    // sidebar spinner that does linger must be on the currently
    // selected env.
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(1);

    await app.sidebar.openEnvironment(tenant, envs[0]!);
    await app.sidebar.openEnvironment(tenant, envs[1]!);

    // Give the second click a moment to land its prepareOpenSelection
    // (which dispatches resetEnvOpening synchronously) and let any
    // first-click spinner clear. The assertion below uses toHaveCount
    // with auto-retry so the test is not racing on a precise timing.
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0, { timeout: 5_000 });
  });

  test('running build/release/push entries surface a labelled spinner that clears on finish', async ({
    app,
    page,
  }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const environment = envs[0]!;

    const sidebar = page.locator('aside').first();
    // Intentionally no "sidebar has zero spinners" precondition: the
    // harness may still be settling an auto-open of the restored env
    // ("Opening <t>/<e>..." is the isOpening spinner), which is unrelated
    // to this test. We instead assert on the *specific* per-command
    // label, which deriveEnvironmentRow gives precedence over the opening
    // label, so the assertions are robust to a concurrent open.

    const emitActivity = (command: string, status: 'running' | 'succeeded') =>
      page.evaluate(
        ({ command, status, tenant, environment }) => {
          const runtime = (
            window as unknown as {
              runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
            }
          ).runtime;
          const now = new Date().toISOString();
          runtime.EventsEmit('activity:state', {
            id: `spinner-spec-${command}`,
            command,
            tenant,
            environment,
            status,
            startedAt: now,
            lastUpdated: now,
            endedAt: status === 'running' ? undefined : now,
            source: 'trace',
            summary: `${command} ${tenant}/${environment}`,
          });
        },
        { command, status, tenant, environment },
      );

    // Each command maps to its own verb in deriveEnvironmentRow's
    // busyLabel; staging a running entry must light a spinner whose
    // accessible name names the operation and the target env.
    for (const { command, verb } of [
      { command: 'build', verb: 'Building' },
      { command: 'release', verb: 'Releasing' },
      { command: 'push', verb: 'Pushing' },
    ]) {
      const labelledSpinner = sidebar.getByRole('status', {
        name: `${verb} ${tenant} / ${environment}`,
      });
      await emitActivity(command, 'running');
      await expect(labelledSpinner).toBeVisible();

      // Finishing the entry removes this command's spinner. Scope the
      // assertion to the command label so a concurrent isOpening spinner
      // on the same row does not make the test flaky.
      await emitActivity(command, 'succeeded');
      await expect(labelledSpinner).toHaveCount(0);
    }
  });
});

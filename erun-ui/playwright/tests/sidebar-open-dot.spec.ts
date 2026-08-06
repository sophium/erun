import { expect, test } from '../fixtures/erunApp.js';

// The per-env open dot signals "this env has tabs open" and closes the env when
// clicked. It is independent of the LOCAL pill and the busy spinner: open and busy
// can coexist. Open/close tests use the seededEnv fixture so their tab churn never
// leaks into the shared baseline rows.

test.describe('sidebar env open dot', () => {
  test('a never-opened env row shows no open dot', async ({ app, seededEnv }) => {
    // Backend sessions persist across specs and boot can reattach them, so baseline
    // rows may legitimately boot with dots. A per-test seeded env is guaranteed
    // never-opened, so its row must stay quiet.
    const row = app.sidebar.envRowButton(seededEnv.tenant, seededEnv.environment).locator('..');
    await expect(row.getByTestId('env-open-dot')).toHaveCount(0);
  });

  test('opening an env mounts the dot; clicking the dot closes the env', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    // Match the close button by its env-specific accessible name so
    // other envs opened across the suite can't collide with this dot.
    const sidebar = page.locator('aside').first();
    const dot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(dot).toBeVisible();

    // Activating the dot closes the env, and must not also trigger the row's
    // openSelection — a keyboard-activated button still fires a bubbling click,
    // so stopPropagation is exercised either way. The POM owns the activation:
    // the dot sits inside an IconTooltip whose content can intercept the press,
    // so it re-activates until the env is actually closed.
    // closeEnvironment already asserts the row went quiet and stayed quiet.
    await app.sidebar.closeEnvironment(tenant, environment);
  });

  // The dot must reflect the env's REAL condition, not just tab
  // presence: green filled circle while running, hollow grey ring while the
  // linked cloud context is stopped OR the runtime is scaled to zero, amber
  // triangle after a failed deploy / abandoned reconnect. Stopped and failed
  // are distinct states — a stopped environment must never read as a failure.
  // Shape + accessible label carry the state (never colour alone). A real
  // stopped EC2 context, scaled-down runtime, or failed deploy cannot be
  // staged headless, so the spec drives the same env-status Wails event the
  // Go side emits — the emission decisions themselves are owned by
  // erun-ui/env_status_test.go (tryReconnect refusal paths) and
  // erun-ui/environment_stop_test.go (the scaled-to-zero refusal).
  test('the dot reflects the real env state: running → stopped → runtime-stopped → failed → running', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    // Let the open settle before driving simulated states. The backend emits its
    // own env-status while an env is settling, and those one-shot emissions are
    // what revert a simulated state mid-assertion. Waiting on the env's first
    // completed idle poll bounds that window by a real event rather than the wall
    // clock, so the retry below only has to absorb genuine overlap instead of the
    // whole settle sequence.
    const idlePolled = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );
    await app.sidebar.openEnvironment(tenant, environment);
    await idlePolled;
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible();
    await expect(dot).toHaveAttribute('data-env-state', 'running');

    // While an env is settling open, the backend legitimately emits its OWN
    // env-status (a session reconnect clears to "", a runtime-ensure that can't
    // reach the undeployed runtime flags "failed"). Those one-shot emissions can
    // land just after a single simulated event and revert the dot — a real race
    // that a faster host happens to win. Re-drive the simulated event until the
    // dot reflects it in BOTH its state attribute and its accessible name, so the
    // assertion is deterministic on any host WITHOUT masking a regression: the dot
    // must still actually reach and hold each state (a broken dot never converges
    // and the step times out). Folding the name check into the retry matters —
    // a delayed backend emission on a loaded host can revert it between a
    // converged attribute check and a separate name assertion.
    const driveEnvStatus = async (
      status: string,
      expectedState: string,
      expectedName: string | RegExp,
    ): Promise<void> => {
      await expect(async () => {
        await emitEnvStatus(page, tenant, environment, status);
        await expect(dot).toHaveAttribute('data-env-state', expectedState, { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(expectedName, { timeout: 1_000 });
      }).toPass({ timeout: 20_000 });
    };

    await driveEnvStatus(
      'stopped',
      'stopped',
      new RegExp(`^${tenant} / ${environment} is stopped`),
    );
    // A runtime scaled to zero is stopped, not broken: it renders the same
    // hollow-ring stopped glyph as a stopped cloud context, but names the other
    // recovery — opening the environment is what wakes the runtime, whereas a
    // stopped cloud context is started from the titlebar. Scaling a real
    // Deployment to zero needs a cluster the headless harness deliberately
    // lacks, so the spec drives the same env-status event the Go side emits;
    // the emission decisions are owned by erun-ui/environment_stop_test.go
    // (TestReconnectRefusedFlagsStoppedRuntimeInsteadOfFailed) and the
    // cluster-state mapping by TestRuntimeStoppedForSelectionMapsClusterStateToTheIndicator.
    await driveEnvStatus(
      'runtime-stopped',
      'runtime-stopped',
      /is stopped — click it in the sidebar/,
    );
    await driveEnvStatus('failed', 'failed', /deploy failed/);
    await driveEnvStatus('', 'running', `Close ${tenant} / ${environment}`);

    // Close the env so the singleton backend returns to its pre-test shape.
    // This step used to be an intermittent red under full-suite load: a session
    // that had already exited made its PTY close report "file already closed",
    // which failed the whole CloseEnvironmentSessions call and aborted the
    // frontend's unwind before it cleared the env's tabs — so the dot stayed
    // mounted. Session close is idempotent now (erun-ui/session.go
    // ignoreAlreadyClosed); what remains is the tooltip swallowing the
    // activation, which the POM's re-activation absorbs.
    // closeEnvironment already asserts the row went quiet and stayed quiet.
    await app.sidebar.closeEnvironment(tenant, environment);
  });
});

// Mirrors the env-status event the Go side emits from tryReconnect's refusal paths
// and the open/respawn clears, so the spec can drive states it cannot stage headless.
async function emitEnvStatus(
  page: import('@playwright/test').Page,
  tenant: string,
  environment: string,
  status: string,
): Promise<void> {
  await page.evaluate(
    (payload) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('env-status', payload);
    },
    { tenant, environment, status },
  );
}

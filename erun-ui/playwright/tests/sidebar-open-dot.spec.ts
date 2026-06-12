import { expect, test } from '../fixtures/erunApp.js';

// sidebar-open-dot covers the per-env green dot that signals
// "this env has tabs open in the desktop" and is clickable to
// close the env (tear down its Local/ERun/AI tabs and stop
// tracking it from the desktop session state). The dot is
// independent of the LOCAL pill (which marks the dev-machine env)
// and of the busy spinner (which fires only while an
// activity-queue entry is running): open and busy are independent
// states and can coexist.
//
// The dot is driven by state.terminal.tabsByEnv[selectionKey]
// having at least one entry. The headless harness exercises the
// same openSelection thunk a real click hits, so the dot mounts
// after openEnvironment and disappears after the close click.
//
// The open/close tests run against a per-test seeded env (the seededEnv
// fixture) so their tab churn never leaks into the shared baseline rows.

test.describe('sidebar env open dot', () => {
  test('a never-opened env row shows no open dot', async ({ app, seededEnv }) => {
    // Backend sessions persist across specs and the boot sequence can
    // reattach them, so baseline rows may legitimately boot with dots. A
    // per-test seeded env is guaranteed never-opened: its row must stay
    // quiet. A regression that always-rendered the dot (e.g. not
    // null-checking the tabsByEnv lookup) would light this row and fail.
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

    // Scope the dot lookup to the same env row to avoid collisions if
    // multiple envs end up open across the suite — the row containing
    // the matching edit button is the row this test owns.
    const sidebar = page.locator('aside').first();
    const dot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(dot).toBeVisible({ timeout: 6_000 });

    // Clicking the dot must not also trigger the row's openSelection.
    // The selected env after the close should NOT remain on the one
    // we just closed; we assert the dot disappears, which is the
    // observable signal that tabsByEnv was cleared.
    await dot.click();
    await expect(dot).toHaveCount(0, { timeout: 6_000 });
  });

  // Issue #470 — the dot must reflect the env's REAL condition, not just tab
  // presence: green filled circle while running, hollow grey ring while the
  // linked cloud context is stopped, amber triangle after a failed deploy /
  // abandoned reconnect. Shape + accessible label carry the state (never
  // colour alone). A real stopped EC2 context or failed deploy cannot be
  // staged headless, so the spec drives the same env-status Wails event the
  // Go side emits — the emission decisions themselves are owned by
  // erun-ui/env_status_test.go (tryReconnect refusal paths, the #331
  // pattern).
  test('the dot reflects the real env state: running → stopped → failed → running', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible({ timeout: 6_000 });
    await expect(dot).toHaveAttribute('data-env-state', 'running');

    await emitEnvStatus(page, tenant, environment, 'stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped', { timeout: 4_000 });
    await expect(dot).toHaveAccessibleName(new RegExp(`^${tenant} / ${environment} is stopped`));

    await emitEnvStatus(page, tenant, environment, 'failed');
    await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 4_000 });
    await expect(dot).toHaveAccessibleName(/deploy failed/);

    await emitEnvStatus(page, tenant, environment, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 4_000 });
    await expect(dot).toHaveAccessibleName(`Close ${tenant} / ${environment}`);

    // Close the env so the singleton backend returns to its pre-test shape.
    await dot.click();
    await expect(dot).toHaveCount(0, { timeout: 6_000 });
  });
});

// emitEnvStatus injects an `env-status` event, mirroring what the Go side
// emits from tryReconnect's refusal paths and the open/respawn clears.
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

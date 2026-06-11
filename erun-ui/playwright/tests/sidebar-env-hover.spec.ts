import { test, expect } from '../fixtures/erunApp.js';

// Issue #437 — hovering a sidebar env row shows a hover card with the env's
// version, the issue it's working on (branch + linked issue title), and its
// current activity, replacing the plain tenant/env tooltip.
//
// The three section labels and the empty/populated states are reachable
// against any config. The branch+title content depends on the env's worktree
// being on the host (local-agent); the dev's real ~/.erun may only have
// remote-worktree envs, so the spec asserts the card structure + that the
// "Working on" section renders *some* resolved state (branch, an availability
// reason, or "Resolving…"), not a specific branch. The populated branch+issue
// path is verified end-to-end against a local-agent fixture in the PR.
test.describe('sidebar env hover card', () => {
  test('hovering an env row opens a card with version, working issue, and activity', async ({
    app,
  }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.hoverEnvironmentRow(tenant, env);

    const card = app.sidebar.envHoverCard(tenant, env);
    await expect(card).toBeVisible({ timeout: 6_000 });

    // All three sections are present.
    await expect(card.getByText('Version', { exact: true })).toBeVisible();
    await expect(card.getByText('Working on', { exact: true })).toBeVisible();
    await expect(card.getByText('Activity', { exact: true })).toBeVisible();

    // The working-issue lookup resolves to a non-empty state (a branch, an
    // availability reason for remote envs, or the transient loading text) —
    // never a blank.
    await expect
      .poll(async () => (await card.locator('dd').nth(1).textContent())?.trim() ?? '')
      .not.toBe('');

    // Issue #462 — whatever it resolves to, it is never the implementation
    // excuse the card used to print for remote envs.
    expect((await card.locator('dd').nth(1).textContent()) ?? '').not.toContain(
      'worktree lives in the pod',
    );
  });

  // Issue #462 — a never-opened env has no pod to be "idle" about: the
  // Activity row must say it is not open, and the Working-on row must offer
  // the next step instead of an implementation excuse. The not-open env is
  // found by scanning for a row without the open dot AND without a busy
  // spinner — a desktop in-flight operation legitimately outranks the
  // not-open state in the Activity row, so a mid-(re)open env (this
  // harness's auto-opened local env, for example) is not a valid target.
  // If every env is open or busy there is nothing to assert against, so the
  // spec skips with the reason recorded.
  test('a not-open env shows "Not open", never "Idle" or the pod excuse', async ({ app }) => {
    const target = await firstQuietNotOpenEnv(app);
    test.skip(target === null, 'every env in this developer harness is open or busy');
    const { tenant, env } = target!;

    await app.sidebar.hoverEnvironmentRow(tenant, env);
    const card = app.sidebar.envHoverCard(tenant, env);
    await expect(card).toBeVisible({ timeout: 6_000 });

    const activity = card.locator('dd').nth(2);
    await expect(activity).toHaveText('Not open', { timeout: 6_000 });

    const workingOn = card.locator('dd').nth(1);
    await expect.poll(async () => (await workingOn.textContent())?.trim() ?? '').not.toBe('');
    expect((await workingOn.textContent()) ?? '').not.toContain('worktree lives in the pod');
  });

  // Issue #462/#470 — an open env whose real state is stopped must say
  // "Stopped", never "Idle". A real stopped cloud context cannot be staged
  // headless, so the spec drives the env-status event the Go side emits
  // (the emission decisions are owned by erun-ui/env_status_test.go).
  test('an open env flagged stopped shows "Stopped" in the Activity row', async ({ app, page }) => {
    // Avoid the local default env: on developer harnesses it churns through
    // auto-(re)open cycles whose busy label legitimately outranks the
    // stopped state in the Activity row.
    const target = await app.sidebar.firstEnvironmentExcludingLocal();
    test.skip(target === null, 'no non-local environment in this developer harness');
    const { tenant, env } = target!;

    await app.sidebar.openEnvironment(tenant, env);
    const dot = app.sidebar.envOpenDot(tenant, env);
    await expect(dot).toBeVisible({ timeout: 6_000 });

    // The open click left the pointer on the row, so hovering it again would
    // be a no-op (no fresh mouseenter) — park the pointer elsewhere first.
    await page.mouse.move(0, 0);
    await app.sidebar.hoverEnvironmentRow(tenant, env);
    const card = app.sidebar.envHoverCard(tenant, env);
    await expect(card).toBeVisible({ timeout: 6_000 });

    // Let the open settle first: the in-flight open's busy label outranks
    // the stopped state by design, and the spawn path itself emits a
    // clearing env-status — an injection raced against it would be
    // overwritten (which is the production contract, not a flake).
    const activity = card.locator('dd').nth(2);
    await expect(activity).toContainText('Idle', { timeout: 15_000 });

    await emitEnvStatusEvent(page, tenant, env, 'stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped', { timeout: 4_000 });
    await expect(activity).toContainText('Stopped', { timeout: 4_000 });

    // Restore the row's healthy state and close the env for later specs.
    await emitEnvStatusEvent(page, tenant, env, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 4_000 });
    await dot.click();
    await expect(dot).toHaveCount(0, { timeout: 6_000 });
  });
});

// emitEnvStatusEvent injects an `env-status` event, mirroring what the Go
// side emits from tryReconnect's refusal paths and the open/respawn clears.
async function emitEnvStatusEvent(
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

// firstQuietNotOpenEnv scans the sidebar for an env row that has neither the
// open dot nor a busy spinner.
async function firstQuietNotOpenEnv(
  app: import('../pages/index.js').AppShell,
): Promise<{ tenant: string; env: string } | null> {
  for (const tenant of await app.sidebar.tenants()) {
    for (const env of await app.sidebar.environmentsFor(tenant)) {
      const row = app.sidebar.envRowButton(tenant, env).locator('..');
      const open = await row.getByTestId('env-open-dot').count();
      const busy = await row.getByRole('status').count();
      if (open === 0 && busy === 0) {
        return { tenant, env };
      }
    }
  }
  return null;
}

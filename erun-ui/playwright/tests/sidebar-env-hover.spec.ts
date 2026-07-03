import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The hover card replaces the plain tenant/env tooltip. Seeded envs aren't git
// worktrees, so "Working on" resolves to an availability reason, never a branch;
// the populated branch+issue path is covered by the Go-side working-issue tests.
test.describe('sidebar env hover card', () => {
  test('hovering an env row opens a card with version, working issue, and activity', async ({
    app,
  }) => {
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);

    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();

    await expect(card.getByText('Version', { exact: true })).toBeVisible();
    await expect(card.getByText('Working on', { exact: true })).toBeVisible();
    await expect(card.getByText('Activity', { exact: true })).toBeVisible();

    await expect
      .poll(async () => (await card.locator('dd').nth(1).textContent())?.trim() ?? '')
      .not.toBe('');

    // Whatever it resolves to, it is never the implementation
    // excuse the card used to print for remote envs.
    expect((await card.locator('dd').nth(1).textContent()) ?? '').not.toContain(
      'worktree lives in the pod',
    );
  });

  // A never-opened env has no pod, so Activity must say "Not open" (never
  // "Idle") and Working-on must offer a next step, not the pod excuse. A
  // per-test seeded env is guaranteed never-opened: fresh on disk, no tabs.
  test('a not-open env shows "Not open", never "Idle" or the pod excuse', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.hoverEnvironmentRow(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);
    await expect(card).toBeVisible();

    const activity = card.locator('dd').nth(2);
    await expect(activity).toHaveText('Not open');

    const workingOn = card.locator('dd').nth(1);
    await expect.poll(async () => (await workingOn.textContent())?.trim() ?? '').not.toBe('');
    expect((await workingOn.textContent()) ?? '').not.toContain('worktree lives in the pod');
  });

  // An open env whose real state is stopped must say "Stopped", never "Idle".
  // A stopped cloud context can't be staged headless, so the spec injects the
  // env-status event; the emission decisions are owned by env_status_test.go.
  test('an open env flagged stopped shows "Stopped" in the Activity row', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible();

    // The open click left the pointer on the row, so hovering it again would
    // be a no-op (no fresh mouseenter) — park the pointer elsewhere first.
    await page.mouse.move(0, 0);
    await app.sidebar.hoverEnvironmentRow(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);
    await expect(card).toBeVisible();

    // Let the open settle first: the in-flight open's busy label outranks
    // the stopped state by design, and the spawn path itself emits a
    // clearing env-status — an injection raced against it would be
    // overwritten (which is the production contract, not a flake). The
    // settled state depends on how far the stub-backed `erun open` gets
    // (the kubectl stub fails its deploy probe), so accept any terminal
    // non-busy state before injecting.
    const activity = card.locator('dd').nth(2);
    await expect(activity).toContainText(/Idle|Stopped|Deploy failed|Not open/, {
      timeout: 15_000,
    });

    await emitEnvStatusEvent(page, tenant, environment, 'stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped');
    await expect(activity).toContainText('Stopped');

    // Reset the injected status and close the env so it doesn't leak into later specs (shared backend).
    await emitEnvStatusEvent(page, tenant, environment, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running');
    await dot.click();
    await expect(dot).toHaveCount(0);
  });
});

// Injecting env-status is a faithful stand-in: the Go side emits the same event
// from tryReconnect's refusal paths and the open/respawn clears.
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

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

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

    // dd(1) is the Erun version row, always present once the seeded env has a
    // runtime version; Working on is dd(2).
    await expect
      .poll(async () => (await card.locator('dd').nth(2).textContent())?.trim() ?? '')
      .not.toBe('');

    // Whatever it resolves to, it is never the implementation
    // excuse the card used to print for remote envs.
    expect((await card.locator('dd').nth(2).textContent()) ?? '').not.toContain(
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

    // dd(1) is the Erun version row, always present once the seeded env has a
    // runtime version; Activity is dd(3), Working on is dd(2).
    const activity = card.locator('dd').nth(3);
    await expect(activity).toHaveText('Not open');

    const workingOn = card.locator('dd').nth(2);
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

    const card = app.sidebar.envHoverCard(tenant, environment);
    // dd(1) is the Erun version row, always present once the seeded env has a
    // runtime version; Activity is dd(3).
    const activity = card.locator('dd').nth(3);
    // hoverAndRead re-opens the card on each retry: an async row re-render (the
    // busy→idle transition, or the injected status changing the dot glyph) drops
    // the pointer and closes the card mid-assert, so a single hover is not stable.
    // Re-parking the pointer guarantees a fresh mouseenter each time.
    const activitySettlesTo = async (matcher: RegExp | string): Promise<void> => {
      await expect(async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(tenant, environment);
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(activity).toContainText(matcher, { timeout: 1_000 });
      }).toPass();
    };

    // Wait for the open to FULLY settle before injecting. The busy label outranks
    // the stopped state by design, and the open/AI spawn actions emit a clearing
    // env-status as they succeed; injecting before that lands would be overwritten
    // (the production contract, not a flake). "Idle" is the settled, non-busy
    // terminal state, after which no further open-settle emission overrides us.
    await activitySettlesTo('Idle');

    await emitEnvStatusEvent(page, tenant, environment, 'stopped');
    await activitySettlesTo('Stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped');

    // Reset the injected status and close the env so it doesn't leak into later specs (shared backend).
    await emitEnvStatusEvent(page, tenant, environment, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running');
    // closeEnvironment already asserts the row went quiet and stayed quiet.
    await app.sidebar.closeEnvironment(tenant, environment);
  });

  // The activity poller (env-activity) and the sticky-condition setter
  // (env-status) run on separate cycles, so a fresh "the environment is busy"
  // observation can land while env-status still says stopped from before —
  // the row's spinner is honest about it, but the card fell back to a bare
  // "Stopped — start it from the titlebar" because the indicator's dot gives
  // the sticky stopped condition priority over busy. The row and the card must
  // never say different things about the same spinner.
  test('a fresh busy observation never lets the card say a bare Stopped', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible();

    const card = app.sidebar.envHoverCard(tenant, environment);
    // dd(1) is the Erun version row, always present once the seeded env has a
    // runtime version; Activity is dd(3).
    const activity = card.locator('dd').nth(3);
    const activitySettlesTo = async (matcher: RegExp | string): Promise<void> => {
      await expect(async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(tenant, environment);
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(activity).toContainText(matcher, { timeout: 1_000 });
      }).toPass();
    };

    await activitySettlesTo('Idle');
    await emitEnvStatusEvent(page, tenant, environment, 'stopped');
    await activitySettlesTo('Stopped');

    // The backend's own sweep runs on a timer against this inert (never
    // deployed) env and will legitimately overwrite the injected busy
    // observation with "unreachable" — re-drive it until the assertion holds,
    // bounded by a real timeout rather than a guessed delay.
    await expect(async () => {
      await emitEnvActivityEvent(page, {
        tenant,
        environment,
        reachable: true,
        observed: true,
        busy: true,
        detail: 'holding: gradle-build',
      });
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(tenant, environment);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(activity).toContainText('holding: gradle-build', { timeout: 1_000 });
      await expect(activity).not.toContainText('Stopped');
    }).toPass({ timeout: 20_000 });

    // Reset the injected state so it doesn't leak into later specs (shared backend).
    await emitEnvActivityEvent(page, {
      tenant,
      environment,
      reachable: false,
      observed: false,
      busy: false,
    });
    await emitEnvStatusEvent(page, tenant, environment, '');
    await app.sidebar.closeEnvironment(tenant, environment);
  });
});

interface EnvActivityEvent {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  outage?: boolean;
  busy: boolean;
  detail?: string;
}

// Mirrors the env-activity event erun-ui/environment_activity.go emits per tick.
async function emitEnvActivityEvent(
  page: import('@playwright/test').Page,
  event: EnvActivityEvent,
): Promise<void> {
  await page.evaluate((payload) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', payload);
  }, event);
}

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

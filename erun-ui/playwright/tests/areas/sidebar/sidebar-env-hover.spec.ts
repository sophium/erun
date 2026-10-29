import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Seeded envs aren't git worktrees, so "Working on" shows an availability reason; the branch path
// is covered by Go tests.
test.describe('sidebar env hover card', () => {
  test('hovering an env row opens a card with version, working issue, and activity', async ({
    app,
  }) => {
    // The default-landing env's auto-open and the activity sweep can drop the card, so hover and
    // reads retry together.
    let workingOn = '';
    await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });

      await expect(card.getByText('Version', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Working on', { exact: true })).toBeVisible({
        timeout: 1_000,
      });
      await expect(card.getByText('Activity', { exact: true })).toBeVisible({ timeout: 1_000 });

      // dd(1) is Erun version; Working on is dd(2).
      workingOn = (await card.locator('dd').nth(2).textContent({ timeout: 1_000 })) ?? '';
      expect(workingOn.trim()).not.toBe('');
    });

    expect(workingOn).not.toContain('worktree lives in the pod');
  });

  // A never-opened env has no pod: Activity says "Not open" and Working on offers a next step.
  test('a not-open env shows "Not open", never "Idle" or the pod excuse', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    let workingOn = '';
    await app.sidebar.readEnvHoverCard(tenant, environment, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });

      // Activity is dd(3), Working on is dd(2).
      const activity = card.locator('dd').nth(3);
      await expect(activity).toHaveText('Not open', { timeout: 1_000 });

      workingOn = (await card.locator('dd').nth(2).textContent({ timeout: 1_000 })) ?? '';
      expect(workingOn.trim()).not.toBe('');
    });
    expect(workingOn).not.toContain('worktree lives in the pod');
  });

  // A stopped cloud context can't be staged headless, so this injects env-status.
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
    // Activity is dd(3).
    const activity = card.locator('dd').nth(3);
    // A row re-render closes the card, so re-hover on each retry.
    const activitySettlesTo = async (matcher: RegExp | string): Promise<void> => {
      await expect(async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(tenant, environment);
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(activity).toContainText(matcher, { timeout: 1_000 });
      }).toPass();
    };

    // Busy outranks stopped and the open emits a clearing env-status, so inject only after Activity
    // settles to Idle.
    await activitySettlesTo('Idle');

    await emitEnvStatusEvent(page, tenant, environment, 'stopped');
    await activitySettlesTo('Stopped');
    await expect(dot).toHaveAttribute('data-env-state', 'stopped');

    // Shared backend: reset the injected status and close the env.
    await emitEnvStatusEvent(page, tenant, environment, '');
    await expect(dot).toHaveAttribute('data-env-state', 'running');
    await app.sidebar.closeEnvironment(tenant, environment);
  });

  // env-activity and env-status run on separate cycles; the card must not say a bare "Stopped"
  // while the row spins.
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
    // Activity is dd(3).
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

    // The backend sweep can overwrite the injected busy state, so re-drive until it holds.
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
    }).toPass();

    // Shared backend: reset the injected state.
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

  test('an environment with no cloud node omits the row entirely', async ({ app, seededEnv }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.readEnvHoverCard(tenant, environment, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Activity', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Cloud node', { exact: true })).toHaveCount(0, {
        timeout: 1_000,
      });
      await expect(card).not.toContainText('not power-managed by erun', { timeout: 1_000 });
    });
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

// Mirrors the env-activity event erun-ui/internal/runtimeview/env_activity.go emits per tick.
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

// The Go side emits the same event from tryReconnect's refusal paths and the open/respawn clears.
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

import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A row must report busy, and be visible when the env is reachable even if the desktop didn't open
// it.
// Neither state is stageable headless, so specs drive the env-activity event; the backend sweep can
// clear it, so assertions re-drive until they converge.

test.describe('sidebar env activity', () => {
  test('a reachable env shows a status light even though the desktop never opened it', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const row = app.sidebar.envRowButton(tenant, environment).locator('..');
    await expect(row.getByTestId('env-open-dot')).toHaveCount(0);

    const dot = app.sidebar.envOpenDot(tenant, environment);
    await driveEnvActivity(
      page,
      { tenant, environment, reachable: true, observed: true, busy: false },
      async () => {
        await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 1_000 });
        // Not opened here, so the dot is a passive light, not a close control.
        await expect(dot).toHaveAttribute('data-env-opened', 'false', { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(
          new RegExp(`^${tenant} / ${environment} is running and in use elsewhere`),
          { timeout: 1_000 },
        );
      },
    );

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: false,
      observed: false,
      busy: false,
    });
    await expect(row.getByTestId('env-open-dot')).toHaveCount(0);
  });

  test('a bound-but-dead forward reads as an outage, not as a quiet row', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A port-forward that holds its port but answers nothing must not render as a green "in use
    // elsewhere" light.
    const { tenant, environment } = seededEnv;
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await driveEnvActivity(
      page,
      { tenant, environment, reachable: true, observed: false, outage: true, busy: false },
      async () => {
        await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(
          new RegExp(`^${tenant} / ${environment} is unreachable —`),
          { timeout: 1_000 },
        );
      },
    );

    await driveEnvActivity(
      page,
      { tenant, environment, reachable: true, observed: false, outage: true, busy: false },
      async () => {
        await app.sidebar.hoverEnvironmentRow(tenant, environment);
        await expect(app.sidebar.envHoverCard(tenant, environment)).toContainText(
          'Unreachable — its connection is dead; deploy it to bring the runtime back',
          { timeout: 1_000 },
        );
      },
    );
  });

  test('a dropped forward reads as an outage, not as a row nobody opened', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A dropped forward frees the port, so reachable is false and the row is carried by the outage
    // alone.
    const { tenant, environment } = seededEnv;
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await driveEnvActivity(
      page,
      { tenant, environment, reachable: false, observed: false, outage: true, busy: false },
      async () => {
        await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(
          new RegExp(`^${tenant} / ${environment} is unreachable —`),
          { timeout: 1_000 },
        );
      },
    );
  });

  test('a busy env renders busy and says what is holding it', async ({ app, page, seededEnv }) => {
    const { tenant, environment } = seededEnv;
    const busy = {
      tenant,
      environment,
      reachable: true,
      observed: true,
      busy: true,
      detail: 'holding: gradle-build',
    };
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await driveEnvActivity(page, busy, async () => {
      await expect(dot).toHaveAttribute('data-env-state', 'busy', { timeout: 1_000 });
      await expect(dot).toHaveAccessibleName(
        `${tenant} / ${environment} is busy — holding: gradle-build`,
        { timeout: 1_000 },
      );
    });

    await driveEnvActivity(page, busy, async () => {
      await app.sidebar.hoverEnvironmentRow(tenant, environment);
      await expect(app.sidebar.envHoverCard(tenant, environment)).toContainText(
        'Busy — holding: gradle-build',
        { timeout: 1_000 },
      );
    });

    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: false,
      observed: false,
      busy: false,
    });
    const row: Locator = app.sidebar.envRowButton(tenant, environment).locator('..');
    await expect(row.getByTestId('env-open-dot')).toHaveCount(0);
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

async function driveEnvActivity(
  page: Page,
  event: EnvActivityEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvActivity(page, event);
    await assertions();
  }).toPass();
}

// Mirrors the env-activity event erun-ui/internal/runtimeview/env_activity.go emits per tick.
async function emitEnvActivity(page: Page, payload: EnvActivityEvent): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', event);
  }, payload);
}

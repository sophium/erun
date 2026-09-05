import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';

// An environment can be under continuous heavy use with nothing rendered for it:
// work inside the pod produced no desktop signal, and an env opened from the CLI
// had no tabs here, so its row stayed blank either way. These specs lock the two
// halves of the fix — a row that reports busy (and with what), and a row that is
// visible because the environment is reachable rather than because the desktop
// opened it.
//
// Neither state can be staged headless: reachability needs a real port-forward
// and a live MCP edge, and busy needs a pod doing work. So the specs drive the
// same env-activity Wails event the Go poller emits. The observation itself is
// owned by erun-ui/environment_activity_test.go (markers → busy + detail) and
// erun-common's lease tests (what makes an env report busy at all); the
// derivation these specs exercise is owned by
// erun-ui/frontend/src/components/app/Sidebar.helpers.test.ts.
//
// The backend's own sweep runs on a timer against these inert envs and reports
// them unreachable, which legitimately clears a simulated state. Every
// assertion is therefore re-driven until it converges, bounded by a real
// timeout rather than a guessed delay — a genuinely broken row never converges
// and the step still fails.

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
        // Not opened here means there is nothing to close, so the indicator is a
        // passive light rather than a control that would do nothing.
        await expect(dot).toHaveAttribute('data-env-opened', 'false', { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(
          new RegExp(`^${tenant} / ${environment} is running and in use elsewhere`),
          { timeout: 1_000 },
        );
      },
    );

    // And it goes back to blank when the environment stops answering.
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
    // The rarer shape. The environment's port-forward still holds its local
    // port, so the desktop's reachability check keeps saying yes while nothing
    // answers through it; the desktop re-established it, that did not help, and
    // the only thing left is to say so. Rendered from reachable alone this is a
    // green "in use elsewhere" light on an environment no client can talk to.
    //
    // The state needs a real port-forward with a dead far end, which the
    // headless harness has no cluster to produce, so the spec drives the same
    // env-activity event the Go sweep emits. The detection and the bounded
    // repair behind the flag are owned by
    // erun-ui/environment_forward_repair_test.go; the derivation is owned by
    // erun-ui/frontend/src/components/app/Sidebar.helpers.test.ts.
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

    // And the hover card says it in prose, with the recovery attached.
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
    // The ordinary shape, and the one with no other tell. A pod replacement
    // makes kubectl exit, so the local port is free and every field except the
    // diagnosis reads exactly like an environment nobody ever opened — which is
    // how a row the operator had open went quiet instead of reporting an
    // outage. Note reachable is false here: the row is carried by the outage
    // alone, so a derivation that gated on reachable would render nothing.
    //
    // Same harness limitation as the sibling above: producing a real dropped
    // forward needs a cluster, so the spec drives the env-activity event the Go
    // sweep emits. The restart and its bound are owned by
    // erun-ui/environment_forward_repair_test.go::TestDroppedForwardIsRestarted
    // and ::TestUnrepairableForwardReportsInsteadOfLooping.
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

    // The hover card is where the operator reads the same thing in prose, which
    // is what tells them why auto-stop is being deferred.
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
  }).toPass({ timeout: 20_000 });
}

// Mirrors the env-activity event erun-ui/environment_activity.go emits per tick.
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

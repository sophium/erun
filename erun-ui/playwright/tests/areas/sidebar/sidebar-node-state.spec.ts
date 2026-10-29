import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A stopped cloud node must be distinguishable from an undetermined status and a missing
// port-forward.
// Specs drive the `env-node` event (internal/runtimeview/node.go); the backend sweep can clear it, so
// assertions re-drive until they converge.

interface NodeEvent {
  tenant: string;
  environment: string;
  node?: { name: string; label?: string; status: string };
}

async function emitEnvNode(page: Page, payload: NodeEvent): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-node', event);
  }, payload);
}

async function driveEnvNode(
  page: Page,
  event: NodeEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvNode(page, event);
    await assertions();
  }).toPass();
}

interface ActivityEvent {
  tenant: string;
  environment: string;
  reachable: boolean;
  observed: boolean;
  busy: boolean;
}

async function emitEnvActivity(page: Page, payload: ActivityEvent): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', event);
  }, payload);
}

const NODE = { name: 'erun-001-020362606330', label: 'erun-001-eu-west-2' };

test.describe('sidebar cloud-node indicator', () => {
  test('an environment on a stopped node shows an indicator naming the node', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const indicator = app.sidebar.envNodeIndicator(tenant, environment);
    await driveEnvNode(
      page,
      { tenant, environment, node: { ...NODE, status: 'stopped' } },
      async () => {
        await expect(indicator).toBeVisible({ timeout: 1_000 });
        await expect(indicator).toHaveAttribute('data-node-state', 'stopped', { timeout: 1_000 });
        // The accessible name is read out of context, so it must name the node, not just "Stopped".
        await expect(indicator).toHaveAccessibleName(
          new RegExp(`^Cloud node ${NODE.label} is stopped`),
          { timeout: 1_000 },
        );
        await expect(indicator).toHaveAccessibleName(/start it from the titlebar/, {
          timeout: 1_000,
        });
      },
    );
  });

  test('an environment with no cloud node shows no node indicator', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Nothing erun power-manages backs this env; a stopped node would be the wrong answer.
    const { tenant, environment } = seededEnv;
    await emitEnvNode(page, { tenant, environment });
    await expect(app.sidebar.envNodeIndicator(tenant, environment)).toHaveCount(0);
  });

  test('a node whose state could not be read reads unknown, never stopped', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const indicator = app.sidebar.envNodeIndicator(tenant, environment);
    await driveEnvNode(page, { tenant, environment, node: { ...NODE, status: '' } }, async () => {
      await expect(indicator).toHaveAttribute('data-node-state', 'unknown', { timeout: 1_000 });
      await expect(indicator).toHaveAccessibleName(/could not be checked/, { timeout: 1_000 });
    });
    // A known-good reading the poller can no longer confirm also reads unknown.
    await driveEnvNode(
      page,
      { tenant, environment, node: { ...NODE, status: 'unknown' } },
      async () => {
        await expect(indicator).toHaveAttribute('data-node-state', 'unknown', { timeout: 1_000 });
      },
    );
  });

  test('a stale node reading never overrides what the environment reports', async ({
    app,
    page,
    seededEnv,
  }) => {
    // An undetermined node reading must not override an answering env.
    const { tenant, environment } = seededEnv;
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(async () => {
      await emitEnvActivity(page, {
        tenant,
        environment,
        reachable: true,
        observed: true,
        busy: false,
      });
      await emitEnvNode(page, { tenant, environment, node: { ...NODE, status: 'unknown' } });
      await expect(dot).toHaveAttribute('data-env-state', 'running', { timeout: 1_000 });
      await expect(app.sidebar.envNodeIndicator(tenant, environment)).toHaveCount(0);
    }).toPass();
  });

  test('the hover card names the node and its state even when the row stays quiet', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A running node adds no row glyph; the card still answers it.
    const { tenant, environment } = seededEnv;
    const card = app.sidebar.envHoverCard(tenant, environment);
    await expect(async () => {
      await emitEnvNode(page, { tenant, environment, node: { ...NODE, status: 'running' } });
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(tenant, environment);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Cloud node', { timeout: 1_000 });
      await expect(card).toContainText(NODE.label, { timeout: 1_000 });
      await expect(card).toContainText('Running', { timeout: 1_000 });
      await expect(app.sidebar.envNodeIndicator(tenant, environment)).toHaveCount(0);
    }).toPass();
  });
});

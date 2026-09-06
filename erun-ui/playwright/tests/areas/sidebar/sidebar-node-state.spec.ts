import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// An environment on a stopped cloud node rendered NO indicator at all —
// indistinguishable from one whose status simply could not be determined, and
// from one whose port-forward never came up. All three read as "nothing to say",
// and they need different actions. The node's power state was already measured
// and cached per cloud context (cloud_context_cache.go) and simply never carried
// per environment.
//
// These specs lock the rendered half of the fix. A real stopped EC2 instance
// needs a cloud account the headless harness deliberately lacks, so they drive
// the same `env-node` Wails event erun-ui/environment_node.go publishes; what
// that event says for a given cache state is owned by
// erun-ui/environment_node_test.go, and the derivation these specs exercise by
// erun-ui/frontend/src/app/environmentNodeState.test.ts.
//
// The backend's own cloud sweep runs on a timer against the seeded envs (which
// have no cloud context, so it reports no node) and can legitimately clear an
// injected reading, so every assertion is re-driven until it converges, bounded
// by a real timeout. A genuinely broken indicator never converges and the step
// still fails.

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
  }).toPass({ timeout: 20_000 });
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
        // The label names the NODE and the way out of the state. It is read out
        // of context (as the indicator's accessible name), so a bare "Stopped"
        // there would be read as the environment being stopped — a different
        // fact with a different remedy.
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
    // The definite answer "nothing erun power-manages backs this environment".
    // Rendering a stopped node for it would be the confident wrong answer.
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
    // The other shape of the same answer: a known-good reading the poller can no
    // longer confirm. Both must read unknown.
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
    // The node indicator is additive. An environment that is answering must keep
    // reading as running even while its node reading is undetermined — a second,
    // weaker claim must not rewrite a definite one.
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
      // And the undetermined node stays quiet, because the row is already
      // saying something.
      await expect(app.sidebar.envNodeIndicator(tenant, environment)).toHaveCount(0);
    }).toPass({ timeout: 20_000 });
  });

  test('the hover card names the node and its state even when the row stays quiet', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A running node adds no row glyph — it is the ordinary case, and a green
    // dot per row would drown the two states that need attention. The card is
    // where "the node is fine, it is the environment that cannot be determined"
    // stays answerable.
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
    }).toPass({ timeout: 20_000 });
  });
});

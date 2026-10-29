import fs from 'node:fs';

import { expect, test } from '../../../fixtures/erunApp.js';
import {
  SEED_ORCHESTRATOR,
  claudeAgents,
  claudeAgentsPath,
  seedClaudeAgent,
} from '../../../fixtures/seedRoot.js';

// The Go side owns the agent lifecycle; this covers only what the rendered app
// shows. The claude stub stands in for the background service.

// The stub's agents file is shared across specs in a worker.
function restoreAgentsFile(snapshot: string | undefined): void {
  if (snapshot === undefined) {
    fs.rmSync(claudeAgentsPath(), { force: true });
    return;
  }
  fs.writeFileSync(claudeAgentsPath(), snapshot);
}

function snapshotAgentsFile(): string | undefined {
  try {
    return fs.readFileSync(claudeAgentsPath(), 'utf8');
  } catch {
    return undefined;
  }
}

test.describe('an orchestrator runs as a background agent named after it', () => {
  test.describe.configure({ mode: 'serial' });

  test('opening it attaches to its agent, and stopping it keeps that agent for the next open', async ({
    app,
  }) => {
    test.setTimeout(60_000);
    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();

    const named = () => claudeAgents().filter((agent) => agent.name === SEED_ORCHESTRATOR);
    await expect.poll(() => named().length).toBe(1);
    const [agent] = named();
    expect(agent?.running).toBe(true);

    await app.tabStrip.closeTab(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect.poll(() => named().map((kept) => kept.running)).toEqual([false]);

    // Only identity is asserted: the harness shell runs no command in the pane, so
    // the stub never sees the `claude attach`.
    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect.poll(() => named().map((kept) => kept.id)).toEqual([agent?.id]);
    await app.tabStrip.closeTab(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
  });

  test('two agents with its name are reported with the remedy, never guessed between', async ({
    app,
  }) => {
    const snapshot = snapshotAgentsFile();
    try {
      for (const id of ['aaaa0001', 'aaaa0002']) {
        seedClaudeAgent({ id, running: true, name: SEED_ORCHESTRATOR });
      }
      await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);

      await expect(app.sidebar.orchestratorsAlert()).toContainText(
        `Claude background agents are named "${SEED_ORCHESTRATOR}"`,
      );
      await expect(app.sidebar.orchestratorsAlert()).toContainText('claude rm <id>');
      await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    } finally {
      restoreAgentsFile(snapshot);
    }
  });
});

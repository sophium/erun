import fs from 'node:fs';

import { expect, test } from '../../../fixtures/erunApp.js';
import {
  SEED_ORCHESTRATOR,
  claudeAgents,
  claudeAgentsPath,
  seedClaudeAgent,
} from '../../../fixtures/seedRoot.js';

// A persisted orchestrator runs as a Claude Code background agent named after
// its orchestrator id, and the desktop's terminal only attaches to it, so the
// agent -- not the desktop -- owns the conversation a `/clear` or a desktop
// restart used to strand. The Go side owns every decision about the agent
// (erun-ui/orchestrator_background_agent_test.go: the exact-name lookup, the
// recreate on changed launch arguments, the post-clear reopen); what only the
// rendered app can show is that opening the row lands on an agent named after
// it, that Stop keeps that agent, and that an ambiguity the desktop must not
// guess at reaches the operator with its remedy. The harness's claude stub
// stands in for the background service (claudeAgentsPath in
// fixtures/seedRoot.ts), so no real agent ever starts.

// The stub's agents file is shared by every spec in a worker, so a case that
// stages agents of its own leaves the file as it found it.
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

    // Opening it again reuses that agent rather than making another. Only its
    // identity is asserted: the harness's deterministic shell runs no command
    // in the pane (resolveLocalShellCommand), so the stub never sees the
    // `claude attach` that would mark the agent running again.
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

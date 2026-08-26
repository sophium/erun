import fs from 'node:fs';
import path from 'node:path';

import { expect, test } from '../fixtures/erunApp.js';
import { isolatedHomeDir, SEED_ORCHESTRATOR } from '../fixtures/seedRoot.js';

// An orchestrator whose session moved to a conversation of its own used to be
// recoverable only by hand: read the transcript directory, work out which file
// was still growing, and write the restart hand-off yourself. This is the
// surface that makes it a click.
//
// The transcripts are staged on disk and the REAL backend lists them (HOME is
// redirected into the suite's isolated root), so this exercises the actual read
// path — the folder taken from the transcript's own recorded cwd, the size, the
// excerpt — rather than a stubbed answer. What the harness cannot reach is a
// conversation labelled "live": that requires a session that ran and reported
// itself through its hooks, and the harness deliberately has no real Claude to
// run. The resolution behind that label (tracked vs stranded vs derived, and the
// refusal to resume another orchestrator's conversation) is covered by
// erun-ui/orchestrator_live_conversation_test.go and
// erun-ui/orchestrator_conversations_test.go; the attach call this surface makes
// is asserted here.

const STALE_CONVERSATION = '35f05bb8-34b6-5a9e-891e-8b6780550a60';
const LIVE_CONVERSATION = '0c01340d-65bd-4ed9-bb9e-91bdff59a6ec';

function stageTranscript(conversationId: string, cwd: string, prompt: string, ageMs: number): void {
  const dir = path.join(isolatedHomeDir(), '.claude', 'projects', '-orchestrators');
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, `${conversationId}.jsonl`);
  const records = [
    JSON.stringify({ type: 'queue-operation', sessionId: conversationId, content: prompt }),
    JSON.stringify({ type: 'user', cwd, sessionId: conversationId, message: { content: prompt } }),
  ];
  fs.writeFileSync(file, `${records.join('\n')}\n`);
  const written = new Date(Date.now() - ageMs);
  fs.utimesSync(file, written, written);
}

function removeStagedTranscripts(): void {
  fs.rmSync(path.join(isolatedHomeDir(), '.claude', 'projects', '-orchestrators'), {
    recursive: true,
    force: true,
  });
}

test.describe('choosing which conversation an orchestrator resumes', () => {
  test.beforeEach(() => {
    // The measured shape of the bug: one conversation that stopped growing ten
    // hours ago, and one written seconds ago.
    stageTranscript(
      STALE_CONVERSATION,
      '/Users/pw/orchestrators',
      'the conversation that went quiet',
      10 * 60 * 60 * 1000,
    );
    stageTranscript(
      LIVE_CONVERSATION,
      '/Users/pw/orchestrators',
      'carry the release through to its verified end',
      14 * 1000,
    );
  });

  test.afterEach(() => {
    removeStagedTranscripts();
  });

  test('the manage dialog lists what the orchestrator can resume and says what it is on', async ({
    app,
  }) => {
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    // The operator's first question — which conversation am I on, and why — is
    // answered in prose before any row is read.
    await expect(app.orchestratorDialog.conversationSummary()).toBeVisible();

    // Both staged conversations are offered, newest first, each with what it
    // takes to recognise it: the folder it was started in, its size, and how it
    // opens.
    const live = app.orchestratorDialog.conversationRow(LIVE_CONVERSATION);
    const stale = app.orchestratorDialog.conversationRow(STALE_CONVERSATION);
    await expect(live).toBeVisible();
    await expect(stale).toBeVisible();
    await expect(live).toContainText('/Users/pw/orchestrators');
    await expect(live).toContainText('carry the release through');
    await expect(app.orchestratorDialog.conversationRows().first()).toHaveAttribute(
      'data-conversation-id',
      LIVE_CONVERSATION,
    );

    // With nothing recorded as live, the orchestrator is on the conversation its
    // name resolves to — and that row says so rather than leaving the operator
    // to infer it from an unmarked list.
    await expect(
      app.orchestratorDialog.conversationRows().filter({ hasText: 'Resumes now' }),
    ).toHaveCount(1);

    await app.page.keyboard.press('Escape');
  });

  test('attaching a conversation restarts the orchestrator on it and closes the dialog', async ({
    app,
    page,
  }) => {
    const attached: unknown[] = [];
    const running = {
      id: SEED_ORCHESTRATOR,
      name: SEED_ORCHESTRATOR,
      environments: [],
      tenants: [],
      directories: [],
      sessionId: 7777,
      status: 'running',
      transient: false,
    };
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: unknown[] };
      // Both answers are stubbed for the same reason: attaching really does
      // spawn the host AI harness, which the inert harness has no binary for, so
      // the session it returns is the one the backend cannot produce here.
      if (body.method === 'AttachOrchestratorConversation') {
        attached.push(body.args);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: running }),
        });
      }
      if (attached.length > 0 && body.method === 'ListOrchestrators') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: [running] }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');
    await app.orchestratorDialog.conversationAttachButton(LIVE_CONVERSATION).click();

    // The dialog gets out of the way, because the result of this action is the
    // session in the terminal pane behind it.
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    expect(attached).toEqual([[SEED_ORCHESTRATOR, LIVE_CONVERSATION, 80, 24]]);
  });
});

import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT } from '../fixtures/seedRoot.js';
import type { Page } from '@playwright/test';

// lockTerminalsForActivity only locks the sessions that already exist when a
// deploy starts. A tab opened after that -- lockNewlyJoinedSessionIfDeployInFlight
// -- used to show no lock overlay at all until some later queue event happened
// to touch it, so an operator who opened a new terminal mid-deploy had no
// indication the runtime it would attach to was mid-rollout.
//
// This drives the real backend path end to end rather than staging a synthetic
// event: the deploy-start trace is written into the real Local shell's PTY, so
// the backend's own output scanner (the same one a real `erun deploy`'s output
// would go through) registers the activity entry, and a brand-new terminal tab
// is opened only after that entry already exists.

interface SessionInputBridge {
  go: { main: { App: { SendSessionInput: (id: number, data: string) => Promise<void> } } };
}

// Routes through the same Wails method a real keystroke sends, but as one
// call instead of one per character -- per-character typing round-trips each
// key over its own HTTP call to the headless bridge, and those can race and
// land out of order. The trailing \r submits the line, matching what Enter
// sends. Single-quoting the line keeps the shell from parsing its literal
// `>` as a redirection operator.
async function runInSession(page: Page, sessionId: number, command: string): Promise<void> {
  await page.evaluate(
    async ({ id, data }) => {
      const bridge = window as unknown as SessionInputBridge;
      await bridge.go.main.App.SendSessionInput(id, data);
    },
    { id: sessionId, data: `${command}\r` },
  );
}

test.describe('a terminal opened after a deploy has already started locks immediately', () => {
  test('the new tab shows the deploy lock overlay on its first paint', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const localSessionId = await app.openEnvironmentTerminal(tenant, environment);
    expect(localSessionId).toBeGreaterThan(0);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);

    const deployLine = `==> Deploying ${tenant}/${environment}`;
    await runInSession(page, localSessionId, `echo '${deployLine}'`);
    await expect(app.terminalPane.rows()).toContainText(deployLine);

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: 15_000 })
      .toBeGreaterThan(initialExtraCount);

    const overlay = page.getByRole('status').filter({ hasText: 'Waiting for deploy to complete' });
    await expect(overlay).toBeVisible();
  });
});

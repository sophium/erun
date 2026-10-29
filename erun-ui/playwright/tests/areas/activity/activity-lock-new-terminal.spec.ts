import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT } from '../../../fixtures/seedRoot.js';
import type { Page } from '@playwright/test';

// Drives the real backend scanner: the deploy trace goes into the Local shell's PTY,
// and the new tab opens only after the activity entry exists.

interface SessionInputBridge {
  go: { main: { App: { SendSessionInput: (id: number, data: string) => Promise<void> } } };
}

// One call rather than per-character typing, whose HTTP calls can land out of order.
// Single quotes keep the shell from treating `>` as a redirection.
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
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT, withTestBudget());

    const deployLine = `==> Deploying ${tenant}/${environment}`;
    await runInSession(page, localSessionId, `echo '${deployLine}'`);
    await expect(app.terminalPane.rows()).toContainText(deployLine, withTestBudget());

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect.poll(() => extraTabs.count(), withTestBudget()).toBeGreaterThan(initialExtraCount);

    const overlay = page.getByRole('status').filter({ hasText: 'Waiting for deploy to complete' });
    await overlay.waitFor({ state: 'visible' });
    await expect(overlay).toBeVisible();

    // Finish the fake deploy, or the entry stays "running" in the shared backend.
    await runInSession(page, localSessionId, `echo '==> Deployed ${tenant}/${environment}'`);
    // Wait for the release so no locked overlay leaks into the next spec.
    await overlay.waitFor({ state: 'hidden' });
    await expect(overlay).toBeHidden();
  });
});

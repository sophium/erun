import type { Locator, Page } from '@playwright/test';

import { disablePopoverEntranceAnimation, expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The Doing row clamps a long shell command to two lines while keeping the full value in the DOM
// and `title`.

const RUNNING_SESSION_ID = 4242;

// Long enough to wrap well past two lines at the card's w-90.
const LONG_SHELL_COMMAND =
  'export GODEBUG=tlsmlkem=0; erun exec job start --tenant erun --environment code1 ' +
  "--id gl2 --name gl2 -- bash -lc 'erun gate list 2>&1 | head -12' 2>&1 | tail -1";

const SHELL_STARTED_AT_UNIX = Math.floor(Date.now() / 1000) - 645;
// A pattern, since the elapsed time is read when the card renders.
const SHELL_PREFIX_PATTERN = /^Shell running for \d+m\d+s:$/;

function snapshot(overrides: Record<string, unknown>) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: true,
    shellCommand: LONG_SHELL_COMMAND,
    shellStartedAtUnix: SHELL_STARTED_AT_UNIX,
    nudgeCount: 0,
    nudgeCapped: false,
    ...overrides,
  };
}

async function stubOrchestratorList(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [body] }),
      });
    }
    await route.continue();
  });
}

function doingRow(card: Locator): Locator {
  return card.locator('dd').filter({ hasText: 'Shell running' });
}

// The innermost span containing the command, without assuming the fix.
function commandElement(card: Locator): Locator {
  return doingRow(card).locator('span').filter({ hasText: LONG_SHELL_COMMAND }).last();
}

interface DoingGeometry {
  // Measured in units of the row's one-line label height.
  commandLines: number;
  // Positive means the clamp engaged.
  commandOverflow: number;
  rowLines: number;
  title: string | null;
  text: string;
}

// One evaluate per element so no re-render interleaves measurements.
async function doingGeometry(card: Locator, command: Locator): Promise<DoingGeometry> {
  const [unit, rowHeight, report] = await Promise.all([
    card.getByText('Doing', { exact: true }).evaluate((el) => el.clientHeight),
    doingRow(card).evaluate((el) => el.clientHeight),
    command.evaluate((el) => ({
      clientHeight: el.clientHeight,
      scrollHeight: el.scrollHeight,
      title: el.getAttribute('title'),
      text: (el.textContent ?? '').trim(),
    })),
  ]);
  return {
    commandLines: report.clientHeight / unit,
    commandOverflow: report.scrollHeight - report.clientHeight,
    rowLines: rowHeight / unit,
    title: report.title,
    text: report.text,
  };
}

test.describe('orchestrator hover card held-length Doing field', () => {
  test('a long background shell command is held to two lines with the full command recoverable', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({}));
    await app.reboot();
    await disablePopoverEntranceAnimation(page);

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const command = commandElement(card);
      await expect(command).toBeVisible();

      const geometry = await doingGeometry(card, command);
      expect(geometry.commandLines).toBeLessThanOrEqual(2);
      expect(geometry.commandOverflow).toBeGreaterThan(0);
      expect(geometry.text).toBe(LONG_SHELL_COMMAND);
      expect(geometry.title).toBe(LONG_SHELL_COMMAND);
      expect(geometry.rowLines).toBeLessThanOrEqual(4);
    });
  });

  test('the readable half leads in the value treatment, with the command as a muted caption', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, snapshot({}));
    await app.reboot();
    await disablePopoverEntranceAnimation(page);

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const row = doingRow(card);
      // The duration stands on its own, outside the clamped element.
      const prefix = row.getByText(SHELL_PREFIX_PATTERN);
      await expect(prefix).toBeVisible();
      const command = commandElement(card);
      await expect(command).toBeVisible();
      await expect(command).toHaveText(LONG_SHELL_COMMAND);
      await expect(prefix).not.toHaveText(LONG_SHELL_COMMAND);
      // The command uses the muted colour; the prefix keeps the value colour.
      const [prefixColor, commandColor] = await Promise.all([
        prefix.evaluate((el) => getComputedStyle(el).color),
        command.evaluate((el) => getComputedStyle(el).color),
      ]);
      expect(commandColor).not.toBe(prefixColor);
    });
  });

  test('a short command is not padded out to the clamp budget', async ({ app, page }) => {
    // The clamp is an upper bound: a one-line command stays one line.
    await stubOrchestratorList(page, snapshot({ shellCommand: 'yarn build' }));
    await app.reboot();
    await disablePopoverEntranceAnimation(page);

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const command = doingRow(card).getByTitle('yarn build');
      await expect(command).toBeVisible();

      const geometry = await doingGeometry(card, command);
      expect(geometry.text).toBe('yarn build');
      expect(geometry.commandLines).toBeLessThanOrEqual(1.05);
      expect(geometry.commandOverflow).toBeLessThanOrEqual(1);
    });
  });
});

import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The orchestrator card's "Doing" row rendered the background shell's raw
// command verbatim, with no clamp of any kind. The command is machine-authored
// and unbounded -- the real one is ~170 characters -- so it wrapped over
// roughly ten lines and pushed the Environments list, the card's most valuable
// content, out of sight. The card's own environment rows had already been
// given truncation for exactly this reason; this field was missed.
//
// Against origin/main the assertions below fail: the command element carries
// no `title` at all, and its rendered height is set by however many lines the
// command happens to wrap to.
//
// What the fix must NOT do is truncate the value itself: the string stays in
// the DOM in full and is recoverable from `title`, because clipping is a
// rendering decision, not a data one. The assertions are paired so a "fix"
// that simply dropped the command fails just as loudly as the defect.

const RUNNING_SESSION_ID = 4242;

// The command observed on the erun-prod orchestrator card, verbatim. Its
// length is the point -- at the card's fixed w-72 it wraps well past two
// lines, so the clamp has something real to hold back.
const LONG_SHELL_COMMAND =
  'export GODEBUG=tlsmlkem=0; erun exec job start --tenant erun --environment code1 ' +
  "--id gl2 --name gl2 -- bash -lc 'erun gate list 2>&1 | head -12' 2>&1 | tail -1";

// 10m45s before the card renders, matching the reported reading.
const SHELL_STARTED_AT_UNIX = Math.floor(Date.now() / 1000) - 645;
const SHELL_PREFIX = 'Shell running for 10m45s:';

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

// Radix's PopoverContent runs an entrance transform on every open, so a
// bounding-box or height read taken right after `toBeVisible` can land
// mid-transition. Mirrors sidebar-hovercard-layout.spec.ts.
async function disablePopoverEntranceAnimation(page: Page): Promise<void> {
  await page.addStyleTag({
    content:
      '[role="dialog"][data-state] { animation: none !important; transform: none !important; }',
  });
}

interface DoingGeometry {
  // Lines the command element actually occupies, measured against the
  // one-line prefix beside it rather than a parsed line-height -- the two
  // share a font treatment, so the prefix is the honest unit for this read.
  commandLines: number;
  // Height the command's content wants minus the height it is given. Positive
  // means the clamp genuinely engaged rather than the string simply fitting.
  commandOverflow: number;
  // The whole Doing row, which is what the defect stretched.
  rowLines: number;
  title: string | null;
  text: string;
}

// Everything read in one evaluate so no re-render can interleave the
// measurements (the card's open state belongs to the hovered row's own React
// state -- see erun-ui/playwright/AGENTS.md's hover-card bullet).
async function doingGeometry(
  row: Locator,
  prefix: Locator,
  command: Locator,
): Promise<DoingGeometry> {
  const [prefixBox, rowBox, report] = await Promise.all([
    prefix.evaluate((el) => ({ height: el.clientHeight })),
    row.evaluate((el) => ({ height: el.clientHeight })),
    command.evaluate((el) => ({
      clientHeight: el.clientHeight,
      scrollHeight: el.scrollHeight,
      title: el.getAttribute('title'),
      text: (el.textContent ?? '').trim(),
    })),
  ]);
  return {
    commandLines: Math.round(report.clientHeight / prefixBox.height),
    commandOverflow: report.scrollHeight - report.clientHeight,
    rowLines: Math.round(rowBox.height / prefixBox.height),
    title: report.title,
    text: report.text,
  };
}

function doingRow(card: Locator): Locator {
  return card.locator('dd').filter({ hasText: 'Shell running' });
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
      const row = doingRow(card);
      // The readable half leads, in the value treatment, on its own line.
      const prefix = row.getByText(SHELL_PREFIX, { exact: true });
      await expect(prefix).toBeVisible();

      // The element carrying the full command in `title` is the clamped one.
      // On origin/main nothing carries a title, so this fails there.
      const command = row.getByTitle(LONG_SHELL_COMMAND);
      await expect(command).toBeVisible();

      const geometry = await doingGeometry(row, prefix, command);
      // Nothing dropped: the whole command is still rendered...
      expect(geometry.text).toBe(LONG_SHELL_COMMAND);
      // ...and still recoverable in full, which is what makes clipping honest.
      expect(geometry.title).toBe(LONG_SHELL_COMMAND);
      // ...held to the two-line budget...
      expect(geometry.commandLines).toBeLessThanOrEqual(2);
      // ...with the clamp genuinely engaged: the content is longer than the
      // box, so this is a real hold-back, not a command that happened to fit.
      expect(geometry.commandOverflow).toBeGreaterThan(0);
      // And the row as a whole is bounded -- unclamped this row alone ran to
      // about ten lines, so four (prefix, gap, two command lines) is a
      // ceiling the defect clears by an order of magnitude.
      expect(geometry.rowLines).toBeLessThanOrEqual(4);
    });
  });

  test('a short command is not padded out to the clamp budget', async ({ app, page }) => {
    // The clamp is an upper bound, not a fixed height: the common case of a
    // one-line command must still render on one line, and it is still
    // recoverable from `title` for consistency with the long case.
    await stubOrchestratorList(page, snapshot({ shellCommand: 'yarn build' }));
    await app.reboot();
    await disablePopoverEntranceAnimation(page);

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const row = doingRow(card);
      const prefix = row.getByText(SHELL_PREFIX, { exact: true });
      await expect(prefix).toBeVisible();
      const command = row.getByTitle('yarn build');
      await expect(command).toBeVisible();

      const geometry = await doingGeometry(row, prefix, command);
      expect(geometry.text).toBe('yarn build');
      expect(geometry.commandLines).toBe(1);
      expect(geometry.commandOverflow).toBeLessThanOrEqual(1);
    });
  });
});

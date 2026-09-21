import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The orchestrator card's "Doing" row rendered the background shell's raw
// command verbatim, with no clamp of any kind. The command is machine-authored
// and unbounded -- the reported one is about 170 characters -- so it wrapped
// that row over roughly ten lines and pushed the Environments list, the card's
// actual subject, out of sight. The card's own environment rows had already
// been given truncation for exactly this reason; this field was missed, so the
// two cards also disagreed on what a long value does.
//
// The first test is the reproduction: it locates the element rendering the
// command in a way that resolves on both the fixed and the unfixed card, and
// measures how many lines it actually occupies. Against the unfixed card it
// occupies however many lines the command wraps to -- the defect -- and the
// assertion fails on that number rather than on a missing locator.
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
// A pattern, not the literal "10m45s" the issue quotes: the elapsed reading is
// taken when the card renders, which is seconds after this module evaluates,
// so the literal is already stale by the time it is asserted against.
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

// Radix's PopoverContent runs an entrance transform on every open, so a
// height read taken right after `toBeVisible` can land mid-transition.
// Mirrors sidebar-hovercard-layout.spec.ts.
async function disablePopoverEntranceAnimation(page: Page): Promise<void> {
  await page.addStyleTag({
    content:
      '[role="dialog"][data-state] { animation: none !important; transform: none !important; }',
  });
}

function doingRow(card: Locator): Locator {
  return card.locator('dd').filter({ hasText: 'Shell running' });
}

// The element that renders the command itself, resolved without assuming the
// fix: on both the fixed and the unfixed card it is the innermost span whose
// text contains the command.
function commandElement(card: Locator): Locator {
  return doingRow(card)
    .locator('span')
    .filter({ hasText: LONG_SHELL_COMMAND })
    .last();
}

interface DoingGeometry {
  // Lines the command element actually occupies. Measured against the row's
  // own one-line label rather than a parsed line-height: the label is present
  // on both cards, and every element here inherits the same line-height, so
  // it is the honest unit for this read.
  commandLines: number;
  // Height the command's content wants minus the height it is given. Positive
  // means a clamp genuinely engaged, rather than a command that happened to fit.
  commandOverflow: number;
  // The Doing row as a whole, which is what the defect stretched.
  rowLines: number;
  title: string | null;
  text: string;
}

// One evaluate per element, so no re-render can interleave measurements (the
// card's open state belongs to the hovered row's own React state -- see
// erun-ui/playwright/AGENTS.md's hover-card bullet).
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
      // The reproduction: the command is held to the two-line budget. On the
      // unfixed card this reads ~5, the number of lines it actually wraps to.
      expect(geometry.commandLines).toBeLessThanOrEqual(2);
      // And the clamp genuinely engaged: the content is longer than the box,
      // so this is a real hold-back and not a command that happened to fit.
      expect(geometry.commandOverflow).toBeGreaterThan(0);
      // Nothing dropped: the command is still rendered in full...
      expect(geometry.text).toBe(LONG_SHELL_COMMAND);
      // ...and still recoverable in full, which is what makes clipping honest.
      expect(geometry.title).toBe(LONG_SHELL_COMMAND);
      // The row as a whole is bounded too -- unclamped it ran to about ten
      // lines, so four (prefix, gap, two command lines) is a ceiling the
      // defect clears by an order of magnitude.
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
      // The duration is the useful part and stands on its own, so it must not
      // be carried inside the clamped command element.
      const prefix = row.getByText(SHELL_PREFIX_PATTERN);
      await expect(prefix).toBeVisible();
      const command = commandElement(card);
      await expect(command).toBeVisible();
      await expect(command).toHaveText(LONG_SHELL_COMMAND);
      await expect(prefix).not.toHaveText(LONG_SHELL_COMMAND);
      // The command recedes rather than leading: the two elements resolve to
      // different rendered colours, so the readable half keeps the card's
      // value treatment while the command drops to the muted one.
      const [prefixColor, commandColor] = await Promise.all([
        prefix.evaluate((el) => getComputedStyle(el).color),
        command.evaluate((el) => getComputedStyle(el).color),
      ]);
      expect(commandColor).not.toBe(prefixColor);
    });
  });

  test('a short command is not padded out to the clamp budget', async ({ app, page }) => {
    // The clamp is an upper bound, not a fixed height: the common case of a
    // one-line command must still render on one line.
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

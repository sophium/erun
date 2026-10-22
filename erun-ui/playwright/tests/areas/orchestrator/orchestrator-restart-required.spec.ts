import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR, SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// erun#1319: re-scoping a running orchestrator changes what it is allowed to
// touch, but its live session keeps whatever MCP toolset it was spawned with —
// a Claude Code session resolves --mcp-config once at launch and cannot be
// rewired in place. Before the fix, the desktop listed the orchestrator as
// "running" with its new environment set and said nothing about the mismatch,
// so the operator had no way to learn their edit had not taken effect. The Go
// side of the fix (which environments a session was actually wired for vs
// what it is configured with now) is covered by
// TestUpdateOrchestratorOnALiveSessionLeavesItsToolsetStale in
// erun-ui/orchestrator_test.go, down to the on-disk MCP config; this spec
// covers the rendering and the control the operator gets to resolve it.
const RUNNING_SESSION_ID = 7373;

function runningStaleSnapshot(overrides: Record<string, unknown> = {}) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' }],
    tenants: [SEED_TENANT],
    directories: ['/tmp/a'],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    restartRequired: true,
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

test.describe('a running orchestrator whose scope changed under it (erun#1319)', () => {
  // A hover card's open state belongs to the row that raised it, and a pointer
  // resting on that row does not raise it again: nothing fires a fresh
  // mouseenter, so a card closed under a stationary pointer stays closed. So a
  // fact already read off the card can be read against a card that is no
  // longer there, which is what this case arranges.
  //
  // The drop used to be staged by the boot's own default-landing open, which
  // handed the environment's terminal focus and took the card down with it, and
  // the case gated the boot's first read to time that. It no longer can: focus
  // moving off a row whose pointer still rests on it is refused while that
  // holder holds it (useHoverCardOpenState's refuseDismissWhileHeld), so the
  // landing leaves the card up. The gating went with it -- a route handler that
  // holds a response back is itself a request still in flight when a short case
  // ends, which fails the run from outside the case -- and the drop is now
  // staged by the dismissal that still closes a card: Escape, pressed with the
  // pointer parked on the row. That also pins the bound on the refusal: an
  // explicit dismissal is not a holder release, and must still close the card.
  //
  // (root AGENTS.md, "A Defect Fix Names Its Reproduction": a probabilistic
  // failure needs a deterministic reproduction constructed on purpose.) Against
  // the pre-fix shape -- one hover, then two independent reads -- this case reds
  // on the second read at expect's 10s default with "element(s) not found" for
  // the card that answered the first; re-hovering the row recovers, which is
  // what readOrchestratorHoverCard wraps into one retryable hover-then-read
  // unit.
  //
  // The reads carry a short bound of their own because they are the probe
  // inside that unit: an attempt that finds no card has to fail quickly for
  // the retry to re-hover while the test still has budget. The unit's own
  // bound is the test's, which is why it is taken bare.
  test('a hover card dropped between two reads is re-hovered, not read as absent', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
    });

    // ...and the card is taken down between the two reads, with the pointer
    // still parked on the row. Asserting the drop is the point of this case
    // rather than an assumption hidden inside it: it is the condition the read
    // below has to survive, and it is also what makes the reproduction
    // deterministic -- the second read starts on the far side of the drop on
    // any machine, not on a slow one.
    await page.keyboard.press('Escape');
    await expect(app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR)).toBeHidden(withTestBudget());

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
      await expect(card).toContainText('Its environments changed while it was running', {
        timeout: 1_000,
      });
    });
  });

  test('the sidebar row and hover card say a restart is required, not plain "running"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    // RED against the pre-fix shape: a plain running dot would also match
    // `Orchestrator ${name} is running`, so asserting the more specific label
    // is what tells the two states apart.
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();

    // One retryable hover-then-read unit, not a hover followed by two
    // independent reads: the card can be dropped between them (see the case
    // above), and a sequence of bare reads has no way back from that.
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
      await expect(card).toContainText('Its environments changed while it was running', {
        timeout: 1_000,
      });
    });
  });

  test('a clean running orchestrator shows neither the dot nor the notice', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot({ restartRequired: false }));
    await app.reboot();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeHidden();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    const card = page.getByRole('dialog', { name: `${SEED_ORCHESTRATOR} details` });
    await expect(card).toBeVisible();
    await expect(card).not.toContainText('Its environments changed while it was running');
  });

  // The restart-required light's accessible name opens with the plain running
  // light's ("... is running but needs a restart ..."), and a role-name query
  // matches substrings unless told otherwise. A locator for the plain running
  // dot therefore resolves to the restart-required one whenever the plain one
  // is absent, and "this row shows a plain running dot" would hold for a row
  // showing the restart one -- the state the test above exists to rule out.
  // Pinned here so the two lights cannot collapse into one assertion again.
  test('a restart-required row does not read as a plain running dot', async ({ app, page }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeHidden();
  });

  test('the manage dialog carries the restart control that resolves it', async ({ app, page }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    await expect(app.orchestratorDialog.restartRequiredNotice()).toBeVisible();
    await expect(app.orchestratorDialog.restartNowButton()).toBeVisible();
    // The footer's own restart action names the remedy too, not just the banner.
    await expect(app.orchestratorDialog.footerRestartButton()).toHaveText('Restart to apply');

    let restartedID = '';
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as {
        method?: string;
        args?: [string?];
      };
      if (parsed.method === 'RestartOrchestrator') {
        restartedID = parsed.args?.[0] ?? '';
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: runningStaleSnapshot({
              sessionId: RUNNING_SESSION_ID + 1,
              restartRequired: false,
            }),
          }),
        });
      }
      await route.continue();
    });

    await app.orchestratorDialog.restartNowButton().click();

    // GREEN: the notice's own action reaches the real restart primitive
    // (RestartOrchestrator — stop the stale session, spawn a fresh one, which
    // re-wires wireOrchestratorMCP for the current scope) rather than a
    // second, weaker mechanism. Closing the dialog is the visible confirmation
    // that the action was taken, not left pending behind a modal.
    await expect.poll(() => restartedID).toBe(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });
});

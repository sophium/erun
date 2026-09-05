import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

// The orchestrator's Environments picker used to drop an ineligible env (an
// unrecognized type) without a trace: it simply was not in the list, which an
// operator who knows the env exists cannot distinguish from "erun has not
// noticed it" or "this is a bug". The picker lists every env it considered,
// disabled with its reason when genuinely ineligible, and the empty state
// names which of the two ways it can be empty applies.
//
// A runtime environment is no longer one of those ineligible cases (erun#1770):
// it is checkable, but offers only the runtime role instead of the usual
// Code/Build picker — see orchestrator-env-role.spec.ts's runtime-role test
// for that coverage. Go coverage for the underlying data (which
// type/role combinations are eligible, what the reason text says) lives in
// TestOrchestratableEnvGatesRuntimeOnTheRuntimeRole and
// TestOrchestratorIneligibilityReasonExplainsEachRejectedType in
// erun-ui/orchestrator_test.go. This spec drives the frontend rendering the
// backend's response produces.

// stubEnvCandidates replaces ListOrchestratorEnvCandidates's response so the
// "several environments, none eligible" empty state is reachable without
// mutating the shared seeded baseline (which always carries eligible
// local-agent envs) — the sanctioned way to stage state the baseline itself
// cannot carry (see erun-ui/playwright/AGENTS.md "Isolated config root and
// seeded baseline").
async function stubEnvCandidates(
  page: Page,
  candidates: {
    tenant: string;
    environment: string;
    eligible: boolean;
    defaultDirectory: string;
    mirrored: boolean;
    ineligibleReason: string;
  }[],
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestratorEnvCandidates') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: candidates }),
      });
      return;
    }
    await route.continue();
  });
}

test.describe('orchestrator Environments picker empty states', () => {
  test('no candidates at all says so and names the remedy', async ({ app, page }) => {
    await stubEnvCandidates(page, []);

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.environmentsEmptyMessage()).toBeVisible();
    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toHaveCount(0);

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });

  test('every candidate ineligible reads differently from no candidates at all', async ({
    app,
    page,
  }) => {
    await stubEnvCandidates(page, [
      {
        tenant: SEED_TENANT,
        environment: 'prod',
        eligible: false,
        defaultDirectory: '',
        mirrored: false,
        ineligibleReason:
          "This environment's type isn't recognized, so it can't be linked to an orchestrator.",
      },
    ]);

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toBeVisible();
    await expect(app.orchestratorDialog.environmentsAllIneligibleMessage()).toContainText('1');
    await expect(app.orchestratorDialog.environmentsEmptyMessage()).toHaveCount(0);

    const checkbox = app.orchestratorDialog.ineligibleEnvCheckbox(SEED_TENANT, 'prod');
    await expect(checkbox).toBeVisible();
    await expect(checkbox).toBeDisabled();
    await expect(app.orchestratorDialog.envIneligibleReason(SEED_TENANT, 'prod')).toContainText(
      "isn't recognized",
    );

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});

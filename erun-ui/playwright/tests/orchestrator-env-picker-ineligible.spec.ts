import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The orchestrator's Environments picker used to drop a runtime env (and any
// other ineligible env) without a trace: it simply was not in the list, which
// an operator who knows the env exists cannot distinguish from "erun has not
// noticed it" or "this is a bug". The picker now lists every env it
// considered, disabled with its reason when ineligible, and the empty state
// names which of the two ways it can be empty applies.
//
// Go coverage for the underlying data (which types are eligible, what the
// reason text says) lives in TestOrchestratableEnvCoversHost,
// TestListOrchestratorEnvCandidatesCoversBothAgentTypes, and
// TestOrchestratorIneligibilityReasonExplainsEachRejectedType in
// erun-ui/orchestrator_test.go. This spec drives the frontend rendering the
// backend's response produces.
test.describe('orchestrator Environments picker accounts for ineligible envs', () => {
  test('a runtime environment is listed disabled with its reason, and cannot be linked', async ({
    app,
    seededRuntimeEnv,
  }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    // The eligible baseline env still offers a plain, checkable row.
    await expect(app.orchestratorDialog.envRow(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();
    await expect(app.orchestratorDialog.envCheckbox(SEED_TENANT, SEED_ENV_ALPHA)).toBeEnabled();

    // The runtime env is considered and shown, not silently omitted: its
    // checkbox is disabled and its reason is a first-class visible line, not
    // hidden behind a tooltip.
    const runtimeCheckbox = app.orchestratorDialog.ineligibleEnvCheckbox(
      SEED_TENANT,
      seededRuntimeEnv.environment,
    );
    await expect(runtimeCheckbox).toBeVisible();
    await expect(runtimeCheckbox).toBeDisabled();
    await expect(runtimeCheckbox).not.toBeChecked();

    const reason = app.orchestratorDialog.envIneligibleReason(
      SEED_TENANT,
      seededRuntimeEnv.environment,
    );
    await expect(reason).toContainText('no worktree to review');
    await expect(reason).toContainText('no in-pod agent to delegate to');

    // A genuinely disabled checkbox refuses a real (non-forced) click outright
    // — Playwright's actionability check is itself the assertion that it
    // never becomes selectable through the UI.
    await expect(runtimeCheckbox.click({ timeout: 1_000 })).rejects.toThrow();
    await expect(runtimeCheckbox).not.toBeChecked();

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});

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
          "Runtime environments have no worktree to review and no in-pod agent to delegate to, so they can't be linked to an orchestrator.",
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
      'no worktree to review',
    );

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});

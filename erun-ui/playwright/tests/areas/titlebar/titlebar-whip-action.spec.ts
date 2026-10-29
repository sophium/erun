import type { Request, Route } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test } from '../../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ENV_BETA,
  SEED_ENV_GAMMA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
} from '../../../fixtures/seedRoot.js';

// WhipNow takes an explicit target list resolved from the picker; nothing
// focused must not fall back to "everything".

// Clicking the theme toggle would dismiss the popover, so apply the `.dark`
// class directly.
async function forceDarkTheme(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
}

// Other specs in the worker leave orchestrators behind, so derive the expected
// count from what the picker rendered checked.
async function whipCountLabel(app: import('../../../pages/index.js').AppShell): Promise<string> {
  const checked = await app.titlebar
    .whipPanel()
    .locator('[role="checkbox"][aria-checked="true"]')
    .count();
  return `Whip ${String(checked)} target${checked === 1 ? '' : 's'}`;
}

// nextWhipNowResponse resolves when the real WhipNow round trip completes.
function nextWhipNowResponse(
  page: import('@playwright/test').Page,
): Promise<import('@playwright/test').Response> {
  return page.waitForResponse((response) => {
    if (!response.url().includes('/__erun_invoke')) {
      return false;
    }
    const body = JSON.parse(response.request().postData() ?? '{}') as { method?: string };
    return body.method === 'WhipNow';
  });
}

async function mockWhipReport(
  page: import('@playwright/test').Page,
  gate: Promise<void>,
  results: Array<{
    kind: string;
    id: string;
    name: string;
    outcome: string;
    reason?: string;
    error?: string;
  }>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'WhipNow') {
      await route.continue();
      return;
    }
    await gate;
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: { results } }),
    });
  });
}

test('the whip control renders a pending state and then every target with its own outcome', async ({
  app,
}) => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await mockWhipReport(app.page, gate, [
    { kind: 'environment', id: 'pw/alpha', name: 'pw/alpha', outcome: 'pushed' },
    {
      kind: 'orchestrator',
      id: 'pw-orch',
      name: 'pw-orch',
      outcome: 'capped',
      reason: 'stopped nudging after repeated silence — reply in its pane or restart it',
    },
    {
      kind: 'environment',
      id: 'pw/beta',
      name: 'pw/beta',
      outcome: 'skipped',
      reason: 'not alive — no live session to push',
    },
    {
      kind: 'environment',
      id: 'pw/gamma',
      name: 'pw/gamma',
      outcome: 'failed',
      reason: 'push failed',
      error: 'writing nudge text: exit status 1',
    },
  ]);

  // Sessions persist across specs in a worker, so focus alpha explicitly.
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

  const whip = app.titlebar.whipButton();
  await expect(whip).toBeVisible();
  await expect(whip).toHaveAttribute('aria-label', /^Whip:/);

  await whip.click();
  // The picker preselects the just-opened environment.
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');

  // The response is gated, so wait on the report replacing the picker.
  await app.titlebar.whipRunButton().click();
  await app.titlebar.waitForWhipReportOpen();

  const body = app.titlebar.whipReportBody();
  await expect(body.getByText('Whipping the selected targets')).toBeVisible();

  release();

  await expect(body.getByText('pw/alpha')).toBeVisible();
  await expect(body.getByText('pw/beta')).toBeVisible();
  await expect(body.getByText('pw/gamma')).toBeVisible();
  await expect(body.getByText('pw-orch')).toBeVisible();
  await expect(body.getByText('Pushed', { exact: true })).toBeVisible();
  await expect(body.getByText('Capped', { exact: true })).toBeVisible();
  await expect(body.getByText('Skipped', { exact: true })).toBeVisible();
  await expect(body.getByText(/stopped nudging after repeated silence/)).toBeVisible();
  await expect(body.getByText(/not alive — no live session to push/)).toBeVisible();
  // exact: the "push failed: ..." detail would also match a substring locator.
  await expect(body.getByText('Failed', { exact: true })).toBeVisible();
  await expect(body.getByText(/push failed.*writing nudge text/)).toBeVisible();

  await app.titlebar.closeWhipReport();
  await expect(app.titlebar.whipReportHeading()).toBeHidden();
});

test('the whip action renders outside the scrollable target list and states the selected count', async ({
  app,
}) => {
  // The action precedes the scrollable list; visibility alone would also pass
  // for an action after the list at this small population.
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  await app.titlebar.openWhipPanel();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');

  const scrollRegion = app.titlebar.whipPanel().locator('.overflow-y-auto').first();
  await expect(scrollRegion.getByRole('button', { name: /^Whip \d+ target/ })).toHaveCount(0);

  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).check();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 2 targets');
});

test('the control stays reachable from an orchestrator tab, not only an environment tab', async ({
  app,
}) => {
  // Whip is global, unlike the env-only titlebar controls.
  await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
  await expect(app.titlebar.whipButton()).toBeVisible();
});

test('defaults to the currently focused environment, with no other target preselected', async ({
  app,
}) => {
  // Focus alpha explicitly; a prior spec's focus can carry over.
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  await app.titlebar.openWhipPanel();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeChecked();
  await expect(
    app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`),
  ).not.toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');
});

test('individually checking another target widens the selection and the report names only what was checked', async ({
  app,
}) => {
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  await app.titlebar.openWhipPanel();
  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).check();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 2 targets');

  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().click();
  await whipped;
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  // Unchecked siblings must not appear.
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toHaveCount(0);
});

test('the nothing-focused case starts from an empty selection, not everything', async ({ app }) => {
  // The tenant dashboard has no default target.
  await app.sidebar.openTenantDashboard(SEED_TENANT);

  await app.titlebar.openWhipPanel();
  await expect(
    app.page.getByText('Nothing is focused right now. Choose one or more targets below.'),
  ).toBeVisible();
  await expect(
    app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`),
  ).not.toBeChecked();
  await expect(
    app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`),
  ).not.toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 0 targets');
  await expect(app.titlebar.whipRunButton()).toBeDisabled();
});

test('select all orchestrators whips only the orchestrator population', async ({ app }) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.openWhipPanel();

  await app.titlebar.selectAllOrchestratorsButton().click();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  await expect(
    app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`),
  ).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));

  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().click();
  await whipped;
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toHaveCount(0);
});

test('select all environments whips only the environment population', async ({ app }) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.openWhipPanel();

  // Environments are per-worker config, so this count is stable.
  await app.titlebar.selectAllEnvironmentsButton().click();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 3 targets');

  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().click();
  await whipped;
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toHaveCount(0);
});

test('select all whips the whole population', async ({ app }) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.openWhipPanel();

  await app.titlebar.selectAllButton().click();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));

  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().click();
  await whipped;
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
});

test('the selection surface is operable end to end without a mouse', async ({ app }) => {
  await app.titlebar.whipButton().focus();
  await app.page.keyboard.press('Enter');
  await app.titlebar.whipPanel().waitFor({ state: 'visible' });
  await expect(app.titlebar.whipRunButton()).toBeVisible();

  await app.titlebar.selectAllButton().focus();
  await app.page.keyboard.press('Enter');
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  // Never a hardcoded total; see whipCountLabel.
  const selectedAll = await whipCountLabel(app);
  await expect(app.titlebar.whipRunButton()).toHaveText(selectedAll);

  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).focus();
  await app.page.keyboard.press('Space');
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));
  await expect(app.titlebar.whipRunButton()).not.toHaveText(selectedAll);

  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().focus();
  await app.page.keyboard.press('Enter');
  await whipped;
  await expect(app.titlebar.whipReportBody()).toBeVisible();
});

// Icon-only shortcuts must carry their tooltip label in their accessible name
// (WCAG 2.5.3 "Label in Name").
test('every group shortcut’s accessible name contains the label its tooltip shows', async ({
  app,
}) => {
  await app.titlebar.openWhipPanel();
  for (const label of [
    'Select all orchestrators',
    'Select all environments',
    'Select all orchestrators and environments',
  ]) {
    const button = app.titlebar.whipPanel().getByRole('button', { name: label, exact: true });
    await expect(button).toBeVisible();

    // Moving between adjacent shortcuts is a tooltip handover; retry hover+read as one unit.
    const tooltip = app.page.getByRole('tooltip', { name: label, exact: true });
    await expect(async () => {
      await app.page.mouse.move(0, 0);
      await button.hover();
      await tooltip.waitFor({ state: 'visible' });

      const visibleLabel = ((await tooltip.textContent()) ?? '').trim();
      const accessibleName = (await button.getAttribute('aria-label')) ?? '';
      expect(accessibleName).toContain(visibleLabel);
    }).toPass();
  }
});

test('renders correctly in both light and dark theme', async ({ app }) => {
  await app.titlebar.openWhipPanel();
  await expect(app.titlebar.whipRunButton()).toBeVisible();
  // animations: 'disabled' finishes the open transition before capture.
  await app.page.screenshot({
    path: artifactPath('test-results/titlebar-whip-action-light.png'),
    animations: 'disabled',
  });

  await forceDarkTheme(app.page);
  await expect(app.titlebar.whipRunButton()).toBeVisible();
  await app.page.screenshot({
    path: artifactPath('test-results/titlebar-whip-action-dark.png'),
    animations: 'disabled',
  });
});

// nextOrchestratorListRefresh resolves on the next ListOrchestrators call.
function nextOrchestratorListRefresh(
  page: import('@playwright/test').Page,
): Promise<import('@playwright/test').Response> {
  return page.waitForResponse((response) => {
    if (!response.url().includes('/__erun_invoke')) {
      return false;
    }
    const body = JSON.parse(response.request().postData() ?? '{}') as { method?: string };
    return body.method === 'ListOrchestrators';
  });
}

// The hover card reads the cached orchestrator list, so a whip must refetch it.
test('a whip pass refreshes the orchestrator list so the hover card is not left stale', async ({
  app,
}) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.openWhipPanel();
  await app.titlebar.selectAllOrchestratorsButton().click();

  const refreshed = nextOrchestratorListRefresh(app.page);
  await app.titlebar.whipRunButton().click();
  await expect(app.titlebar.whipReportHeading()).toBeVisible();
  await refreshed;
});

test('an un-mocked pass names the seeded, never-opened environment and orchestrator as skipped', async ({
  app,
}) => {
  // Nothing in the harness is alive, so the real WhipNow must report every
  // seeded target as skipped, never omitted.
  await app.titlebar.openWhipPanel();
  await app.titlebar.selectAllButton().click();
  const whipped = nextWhipNowResponse(app.page);
  await app.titlebar.whipRunButton().click();
  await whipped;
  await expect(app.titlebar.whipReportHeading()).toBeVisible();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  await expect(body.getByText('Skipped').first()).toBeVisible();
});

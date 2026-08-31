import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ENV_BETA,
  SEED_ENV_GAMMA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
} from '../fixtures/seedRoot.js';

// erun-ui/whip.go's WhipNow used to take no target argument at all: one click
// pushed every configured environment plus every live orchestrator, most of
// which were never even whippable. WhipNow now takes an explicit target list,
// and the titlebar control resolves it from a real selection surface rather
// than fanning out unconditionally (erun#1700). This spec covers: the default
// single-target case (whatever the sidebar has focused), individual
// selection, the three group-select shortcuts, the count the primary action
// states before it acts, the nothing-focused case (which must not fall back
// to "everything"), and that the eventual report lists only what was
// actually targeted.

// Applies the app's own class-based light/dark mechanism directly (root
// AGENTS.md's Design-Language Decision Record: one shared `.dark` class
// mechanism) rather than clicking the titlebar's theme toggle. Unlike a Radix
// Dialog, this popover does not mark the rest of the app aria-hidden, so the
// toggle button is reachable -- but Radix Popover's default dismissable layer
// still closes the popover on any outside pointer interaction, including a
// click on that now-reachable button, taking the whole surface being
// screenshotted with it. manage-dialog-status-badge.spec.ts established this
// same escape hatch for the Dialog case; this is the Popover-shaped sibling
// of that same problem.
async function forceDarkTheme(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
}

// Other specs in this suite create their own orchestrators (e.g.
// orchestrator-link-local-agent.spec.ts, orchestrator-pacing-nudge.spec.ts),
// and backend-side sessions persist across specs within the same worker
// (playwright/AGENTS.md) -- so the *total* live population a "select all"
// resolves against is not under this spec's control and must never be
// hardcoded. whipCountLabel derives the expected button text from what the
// picker itself rendered as checked, so the assertion stays true regardless
// of how many extra orchestrators another spec left behind.
async function whipCountLabel(app: import('../pages/index.js').AppShell): Promise<string> {
  const checked = await app.titlebar
    .whipPanel()
    .locator('[role="checkbox"][aria-checked="true"]')
    .count();
  return `Whip ${String(checked)} target${checked === 1 ? '' : 's'}`;
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

  // Backend-side sessions persist across specs within the same worker
  // (playwright/AGENTS.md), so the sidebar's focus at boot cannot be assumed
  // -- open SEED_ENV_ALPHA explicitly so the default-target resolution below
  // is deterministic regardless of what an earlier spec in this worker left
  // focused.
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

  const whip = app.titlebar.whipButton();
  await expect(whip).toBeVisible();
  await expect(whip).toHaveAttribute('aria-label', /^Whip:/);

  await whip.click();
  // The picker preselects the just-opened environment, so the primary action
  // is already enabled with no further selection needed.
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');
  await app.titlebar.whipRunButton().click();

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
  // A failed push is its own outcome, distinct from a benign skip: same
  // decision (nudge) as a pushed row, but refused rather than quiet. exact:
  // the row's own status word, not the detail line beneath it -- this change
  // adds a "push failed: ..." detail that getByText matches
  // case-insensitively, so a substring locator resolves to both.
  await expect(body.getByText('Failed', { exact: true })).toBeVisible();
  await expect(body.getByText(/push failed.*writing nudge text/)).toBeVisible();

  await app.titlebar.closeWhipReport();
  await expect(app.titlebar.whipReportHeading()).toBeHidden();
});

test('the whip action renders outside the scrollable target list and states the selected count', async ({
  app,
}) => {
  // erun#1748: the action used to be a sibling AFTER the scrollable target
  // list, so a long enough population pushed it below the fold. It now lives
  // in the popover header, a sibling BEFORE the scrollable region -- assert
  // the structure directly (not just that it's visible today, which would
  // also pass with the old, buggy layout at this small seeded population).
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  await app.titlebar.whipButton().click();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');

  const scrollRegion = app.titlebar.whipPanel().locator('.overflow-y-auto').first();
  await expect(scrollRegion.getByRole('button', { name: /^Whip \d+ target/ })).toHaveCount(0);

  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).check();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 2 targets');
});

test('the control stays reachable from an orchestrator tab, not only an environment tab', async ({
  app,
}) => {
  // Several titlebar controls (Titlebar.Controls.tsx's TitlebarEnvControls)
  // render only for an environment tab and disappear in orchestrator mode.
  // Whip is global -- it must not follow that pattern -- so this
  // opens an orchestrator session first and asserts the button is still
  // there, which is the regression this spec exists to catch.
  await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
  await expect(app.titlebar.whipButton()).toBeVisible();
});

test('defaults to the currently focused environment, with no other target preselected', async ({
  app,
}) => {
  // Explicitly focus SEED_ENV_ALPHA rather than relying on the boot default,
  // which can carry a prior spec's focus over within the same worker
  // (playwright/AGENTS.md). Opening the popover must preselect exactly that
  // one target -- neither the sibling seeded environments nor the seeded
  // orchestrator -- and state the count on the primary action before it acts.
  await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
  await app.titlebar.whipButton().click();
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
  await app.titlebar.whipButton().click();
  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).check();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 2 targets');

  await app.titlebar.whipRunButton().click();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  // The report lists only what was targeted: the sibling environments were
  // never checked, so they must not appear even though they are real,
  // configured, and eligible.
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toHaveCount(0);
});

test('the nothing-focused case starts from an empty selection, not everything', async ({ app }) => {
  // The tenant dashboard is neither an environment nor an orchestrator
  // session, so selectWhipDefaultTarget resolves to null here -- this must
  // not fall back to "select every configured target" (erun#1700's core
  // regression).
  await app.sidebar.openTenantDashboard(SEED_TENANT);

  await app.titlebar.whipButton().click();
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
  await app.titlebar.whipButton().click();

  await app.titlebar.selectAllOrchestratorsButton().click();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  await expect(
    app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`),
  ).not.toBeChecked();
  // The count matches exactly whatever the picker rendered checked -- never a
  // hardcoded total, since another spec in this worker can leave its own
  // orchestrator behind (playwright/AGENTS.md's worker-shared backend state).
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));

  await app.titlebar.whipRunButton().click();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toHaveCount(0);
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toHaveCount(0);
});

test('select all environments whips only the environment population', async ({ app }) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.whipButton().click();

  // The seeded baseline carries three environments (alpha, beta, gamma);
  // environments are per-test-worker config, not cross-spec live state, so
  // this population is stable -- unlike the orchestrator count below.
  await app.titlebar.selectAllEnvironmentsButton().click();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText('Whip 3 targets');

  await app.titlebar.whipRunButton().click();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toHaveCount(0);
});

test('select all whips the whole population', async ({ app }) => {
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.titlebar.whipButton().click();

  await app.titlebar.selectAllButton().click();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeChecked();
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));

  await app.titlebar.whipRunButton().click();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_BETA}`)).toBeVisible();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_GAMMA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
});

test('the selection surface is operable end to end without a mouse', async ({ app }) => {
  // Reach and open the trigger by keyboard first, then drive every control in
  // the picker by focus + key press rather than .click() -- the review
  // surface's own keyboard-model precedent (erun-ui/AGENTS.md's Design-
  // Language Decision Record) for what "fully keyboard operable" looks like
  // in a spec.
  await app.titlebar.whipButton().focus();
  await app.page.keyboard.press('Enter');
  await expect(app.titlebar.whipRunButton()).toBeVisible();

  await app.titlebar.selectAllButton().focus();
  await app.page.keyboard.press('Enter');
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).toBeChecked();
  // Never a hardcoded total -- another spec in this worker can leave its own
  // orchestrator behind (playwright/AGENTS.md's worker-shared backend state).
  const selectedAll = await whipCountLabel(app);
  await expect(app.titlebar.whipRunButton()).toHaveText(selectedAll);

  // Unchecking one box by keyboard (Space, the ARIA checkbox pattern's own
  // key) narrows the count back down by exactly one.
  await app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR).focus();
  await app.page.keyboard.press('Space');
  await expect(app.titlebar.whipTargetCheckbox(SEED_ORCHESTRATOR)).not.toBeChecked();
  await expect(app.titlebar.whipRunButton()).toHaveText(await whipCountLabel(app));
  await expect(app.titlebar.whipRunButton()).not.toHaveText(selectedAll);

  await app.titlebar.whipRunButton().focus();
  await app.page.keyboard.press('Enter');
  await expect(app.titlebar.whipReportBody()).toBeVisible();
});

test('renders correctly in both light and dark theme', async ({ app }) => {
  await app.titlebar.whipButton().click();
  await expect(app.titlebar.whipRunButton()).toBeVisible();
  // animations: 'disabled' finishes the popover's own open transition (and any
  // other running CSS animation/transition) before capturing, so the shot is
  // never a frozen mid-fade frame -- deterministic without a wall-clock wait.
  await app.page.screenshot({
    path: 'test-results/titlebar-whip-action-light.png',
    animations: 'disabled',
  });

  await forceDarkTheme(app.page);
  await expect(app.titlebar.whipRunButton()).toBeVisible();
  await app.page.screenshot({
    path: 'test-results/titlebar-whip-action-dark.png',
    animations: 'disabled',
  });
});

test('an un-mocked pass names the seeded, never-opened environment and orchestrator as skipped', async ({
  app,
}) => {
  // The closest observable invariant reachable without a live pod or a
  // running orchestrator session (playwright/AGENTS.md): nothing in this
  // headless harness is alive, so every seeded target must come back
  // skipped, named, with a reason -- never silently omitted. This is the
  // real backend call, unmocked, proving the button is actually wired to
  // erun-ui/whip.go's WhipNow rather than only rendering mocked data.
  await app.titlebar.whipButton().click();
  await app.titlebar.selectAllButton().click();
  await app.titlebar.whipRunButton().click();
  await expect(app.titlebar.whipReportHeading()).toBeVisible();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible();
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible();
  await expect(body.getByText('Skipped').first()).toBeVisible();
});

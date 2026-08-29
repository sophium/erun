import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun-ui/whip.go's WhipNow is the desktop's only binding for `erun whip`:
// before it the button did not exist at all -- an operator looking for a way
// to push a stalled orchestrator or environment agent by hand had no control
// anywhere in the app. This spec covers the
// control itself: it is reachable regardless of which tab is active (whip is
// global, not env-scoped), a click shows a pending state, and the report
// names every target with its own outcome -- pushed, capped, or skipped with
// a reason -- rather than the pass running and showing nothing (root
// AGENTS.md's "Smooth, Seamless, No Dead Ends").

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
  page,
}) => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await mockWhipReport(page, gate, [
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

  const whip = app.titlebar.whipButton();
  await expect(whip).toBeVisible();
  await expect(whip).toHaveAttribute('aria-label', /^Whip:/);

  await whip.click();
  await expect(app.titlebar.whipReportHeading()).toBeVisible();
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText('Pushing every live orchestrator and environment')).toBeVisible();

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
  // A failed push is its own outcome, distinct from a benign skip ():
  // same decision (nudge) as a pushed row, but refused rather than quiet.
  // exact: the row's own status word, not the detail line beneath it — this
  // change adds a "push failed: ..." detail that getByText matches
  // case-insensitively, so a substring locator resolves to both.
  await expect(body.getByText('Failed', { exact: true })).toBeVisible();
  await expect(body.getByText(/push failed.*writing nudge text/)).toBeVisible();

  await app.titlebar.closeWhipReport();
  await expect(app.titlebar.whipReportHeading()).toBeHidden();
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

test('an un-mocked pass names the seeded, never-opened environment and orchestrator as skipped', async ({
  app,
}) => {
  // The closest observable invariant reachable without a live pod or a
  // running orchestrator session (playwright/AGENTS.md): nothing in this
  // headless harness is alive, so every seeded target must come back
  // skipped, named, with a reason -- never silently omitted. This is the
  // real backend call, unmocked, proving the button is actually wired to
  // erun-ui/whip.go's WhipNow rather than only rendering mocked data.
  // A real pass does real work, and its two halves are not equally fast: the
  // environment half lists configs, while the orchestrator half reconciles
  // pacing for every row. The orchestrator rows are therefore the last thing
  // to land, and on a machine busy running the rest of the suite in parallel
  // the default expect budget is too tight for them -- observed as the report
  // rendering with its environment row present and the orchestrator row not
  // yet. These assertions are about a target being named at all, never about
  // how quickly, so they get a budget that reflects that.
  const whipReportTimeout = 30_000;
  await app.titlebar.whipButton().click();
  await expect(app.titlebar.whipReportHeading()).toBeVisible({ timeout: whipReportTimeout });
  const body = app.titlebar.whipReportBody();
  await expect(body.getByText(`${SEED_TENANT}/${SEED_ENV_ALPHA}`)).toBeVisible({
    timeout: whipReportTimeout,
  });
  await expect(body.getByText(SEED_ORCHESTRATOR)).toBeVisible({ timeout: whipReportTimeout });
  await expect(body.getByText('Skipped').first()).toBeVisible({ timeout: whipReportTimeout });
});

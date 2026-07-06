import type { Page, Route, Request } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

// Two version lines from different sources — the environment's own tenant runtime
// (pw-devops) and the upstream ERun runtime (erun-devops) — as the desktop merges
// them for a tenant env. 1.0.16 also drives the ERUN_CHART_AVAILABILITY_OVERRIDE
// per-version chart probe so the components panel populates when it is picked.
async function stubTwoSourceVersions(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadVersionSuggestions') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            suggestions: [
              {
                label: 'Latest stable',
                version: '1.0.16',
                source: 'pw',
                image: 'ghcr.io/sophium/pw-devops',
              },
              {
                label: 'Latest stable',
                version: '1.0.0',
                source: 'ERun',
                image: 'ghcr.io/sophium/erun-devops',
              },
            ],
            notices: [],
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog — version picker (#756)', () => {
  test('groups the two version lines by source so they are distinguishable', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A tenant env's picker merges its own tenant runtime line with the upstream
    // ERun line; both can carry the same "latest stable" label. Grouping by source
    // labels each so the operator can tell the tenant stack from the vanilla runtime
    // (recognition over recall) — both lines stay selectable.
    await stubTwoSourceVersions(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();

    const popover = app.manageDialog.versionPickerPopover();
    await expect(popover.getByText('This environment (pw)')).toBeVisible();
    await expect(popover.getByText('Upstream ERun')).toBeVisible();
    // Both lines remain offered — grouping labels them, it does not drop either.
    await expect(page.getByRole('option', { name: /1\.0\.16/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toBeVisible();
  });

  test('a version picked during an in-flight suggestions fetch is not clobbered', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Regression: the dialog opens at version '' and fires the ~1s suggestions
    // fetch; the global suggestions list still holds the previous open's versions,
    // so the operator can pick one before this open's fetch lands. Before the fix,
    // that fetch's post-processing auto-reselected suggestions[0] — undefined for an
    // unlistable registry — which reset the version to '' and collapsed the
    // "Components to deploy" panel to its gated hint with no error. The fix never
    // overrides an operator's pick.
    const runtimeName = `${seededEnv.tenant}-devops`;
    let calls = 0;
    let releaseSecond: () => void = () => undefined;
    const secondHeld = new Promise<void>((resolve) => {
      releaseSecond = resolve;
    });
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        calls += 1;
        if (calls === 1) {
          // First open populates the global suggestions store with 1.0.0.
          return route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: { suggestions: [{ label: 'Current', version: '1.0.0' }], notices: [] },
            }),
          });
        }
        // Second open: hold the fetch so the stale 1.0.0 can be picked while it is
        // in flight, then resolve it with an empty list (an unlistable registry) —
        // the exact case that reset the version before the fix.
        await secondHeld;
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { suggestions: [], notices: [] } }),
        });
      }
      await route.continue();
    });

    // First open resolves suggestions into the global store, then closes.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    // Second open: its suggestions fetch is held, so the picker shows the stale
    // 1.0.0. Pick it and confirm the components panel populates.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await app.manageDialog.pickVersion('1.0.0');
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();

    // Release the held fetch, which returns an empty list; wait for it to apply
    // (the stale 1.0.0 option drops out of the picker) — a real completion signal,
    // not a wall-clock guess.
    releaseSecond();
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toHaveCount(0);

    // The operator's pick survives: the panel stays populated and never re-gates.
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();
    await expect(app.manageDialog.deployComponentsHint()).toHaveCount(0);
    await expect(app.manageDialog.runtimeVersionInput()).toHaveValue('1.0.0');
  });
});

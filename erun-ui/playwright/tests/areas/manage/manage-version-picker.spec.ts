import type { Page, Route, Request } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

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

  test("reopening the picker resets to the env's own suggestions, not a prior open's (#767)", async ({
    app,
    page,
    seededEnv,
  }) => {
    // Regression: version suggestions are dialog-owned and reset on open, so a
    // reopen never shows a previous open's versions while its own fetch is in
    // flight. That stale carryover in the shared store was what a build's
    // environments-changed delta rewrote, clobbering the picker to the upstream
    // fallback.
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
          return route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: { suggestions: [{ label: 'Latest stable', version: '2.0.0' }], notices: [] },
            }),
          });
        }
        // Second open: hold the fetch so the picker's pre-fetch state is
        // observable — it must show none of the first open's 2.0.0.
        await secondHeld;
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { suggestions: [], notices: [] } }),
        });
      }
      await route.continue();
    });

    // First open resolves 2.0.0 into this dialog's own suggestions.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /2\.0\.0/ })).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    // Reopen with the suggestions fetch held: the picker is open (heading shown)
    // but shows none of the prior open's 2.0.0 — the dialog reset its own state.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /2\.0\.0/ })).toHaveCount(0);

    // Releasing the held (empty) fetch keeps it empty — still no stale 2.0.0.
    const suggestionsDone = page.waitForResponse(
      (resp) =>
        resp.url().includes('__erun_invoke') &&
        (JSON.parse(resp.request().postData() ?? '{}') as { method?: string }).method ===
          'LoadVersionSuggestions',
    );
    releaseSecond();
    await suggestionsDone;
    await expect(page.getByRole('option', { name: /2\.0\.0/ })).toHaveCount(0);
  });

  test('an environments-changed reload does not clobber the open picker (#767)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Regression: version suggestions used to live in the shared tenants slice, which
    // every environments-changed delta (fired constantly while a tenant builds)
    // rewrote from LoadState's suggestions for the sidebar-selected env — clobbering
    // an open picker down to the upstream fallback so a tenant env showed no tenant
    // versions or charts. The dialog now owns its suggestions and the delta no longer
    // touches them. Here the dialog resolves the tenant's pw-devops 1.0.16; the
    // reload returns a distinct 9.9.9 that must NOT reach the picker.
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
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
              ],
              notices: [],
            },
          }),
        });
      }
      if (body.method === 'LoadState') {
        // The delta reload recomputes suggestions for the selected env; return a
        // distinct value the pre-fix code would have pushed into the shared slice.
        const original = await route.fetch();
        const payload = (await original.json()) as {
          data?: { versionSuggestions?: unknown; versionSuggestionNotices?: unknown };
        };
        if (payload.data) {
          payload.data.versionSuggestions = [
            {
              label: 'Latest stable',
              version: '9.9.9',
              source: 'ERun',
              image: 'ghcr.io/sophium/erun-devops',
            },
          ];
          payload.data.versionSuggestionNotices = [];
        }
        return route.fulfill({ contentType: 'application/json', body: JSON.stringify(payload) });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /1\.0\.16/ })).toBeVisible();

    // Fire the exact event a build/deploy fires, and wait for the reload it triggers
    // to actually complete (a real signal, not a wall-clock guess) before asserting.
    const reloadDone = page.waitForResponse(
      (resp) =>
        resp.url().includes('__erun_invoke') &&
        (JSON.parse(resp.request().postData() ?? '{}') as { method?: string }).method ===
          'LoadState',
    );
    await page.evaluate(() => {
      (
        window as unknown as { runtime: { EventsEmit: (name: string, ...a: unknown[]) => void } }
      ).runtime.EventsEmit('environments-changed');
    });
    await reloadDone;

    // The tenant version survives; the reload's 9.9.9 never reaches this picker.
    await expect(page.getByRole('option', { name: /1\.0\.16/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /9\.9\.9/ })).toHaveCount(0);

    // The app keeps reloading state on its own timer, so a later LoadState can
    // still be inside this handler's round-trip when the context tears down —
    // which surfaces as a failure for a test whose assertions already passed.
    // Drain the handlers first rather than racing teardown.
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  });

  test('saving a container registry re-queries the picker so the new registry contributes (#995)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Regression: the picker was loaded only when the dialog opened, and a save
    // invalidated neither it nor the RTK tag it provides. Adding a registry on
    // the General tab therefore left the Runtime tab listing versions from the
    // pre-save registry set — with no entry AND no notice for the registry just
    // added, which reads as "erun cannot use this registry" rather than "this
    // list is stale". Visibility of system status: the save's effect must show
    // in the place it takes effect, without reopening the dialog.
    let calls = 0;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        calls += 1;
        // Before the save only the upstream line resolves; after it, the newly
        // marked registry contributes its own version too.
        const suggestions =
          calls === 1
            ? [{ label: 'Latest stable', version: '1.0.0', source: 'ERun', image: 'erun-devops' }]
            : [
                { label: 'Latest stable', version: '1.0.0', source: 'ERun', image: 'erun-devops' },
                {
                  label: 'Latest stable',
                  version: '4.5.6',
                  source: 'pw',
                  image: 'registry.internal/pw/pw-devops',
                },
              ];
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { suggestions, notices: [] } }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /4\.5\.6/ })).toHaveCount(0);

    // Add a second registry on the General tab. It is deploy-only: a second
    // build-marked registry would be invalid, and discovery reads the host
    // regardless of the roles it carries.
    await app.manageDialog.selectTab('General');
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await page.keyboard.press('Escape');
    await app.manageDialog.registryRoleCheckbox(1, 'build').click();
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    // The re-query is the save's own consequence, so wait for that response —
    // a real signal, not a wall-clock guess. Without the fix it never arrives.
    const requeried = page.waitForResponse(
      (resp) =>
        resp.url().includes('__erun_invoke') &&
        (JSON.parse(resp.request().postData() ?? '{}') as { method?: string }).method ===
          'LoadVersionSuggestions',
    );
    await app.manageDialog.save();
    await requeried;

    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /4\.5\.6/ })).toBeVisible();
    // The version the operator had available before the save stays offered.
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toBeVisible();
  });

  test('deploying a version sends its runtime image so the deploy targets that erun-devops version (#792)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Regression: the runtime image for the deployed version must resolve from the
    // dialog-owned suggestion list the picker renders, not the shared tenants
    // slice (written for the sidebar-selected env, clobbered by env-change deltas).
    // Editing the version field clears the picked image, so if resolution fell
    // back to the shared slice the deploy would omit --runtime-image and silently
    // install the local umbrella's pinned erun-devops version instead of the one
    // the operator targeted. The image reaching StartDeploySession is the proof it
    // resolved from the dialog list.
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              suggestions: [
                {
                  label: 'Upstream ERun',
                  version: '1.0.134',
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

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    // Edit the version field directly — the operator's path — which clears the
    // picked image; resolution must still find it in the dialog's own list.
    await app.manageDialog.runtimeVersionInput().fill('1.0.134');
    await expect(app.manageDialog.deployButton()).toBeEnabled();

    const deployRequest = page.waitForRequest(
      (req) =>
        req.url().includes('/__erun_invoke') &&
        (req.postData() ?? '').includes('StartDeploySession'),
    );
    await app.manageDialog.deploy();
    const payload = (await deployRequest).postData() ?? '';
    // Carries the erun-devops image → buildDeployArgs adds --runtime-image → the
    // deploy routes to the published erun-devops chart at the targeted version.
    expect(payload).toContain('ghcr.io/sophium/erun-devops');
  });
});

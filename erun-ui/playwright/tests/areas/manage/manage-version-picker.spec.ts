import type { Page, Route, Request } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// 1.0.16 also keys ERUN_CHART_AVAILABILITY_OVERRIDE so the components panel populates.
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
    // Both lines can share the "latest stable" label, so each is grouped by source.
    await stubTwoSourceVersions(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();

    const popover = app.manageDialog.versionPickerPopover();
    await expect(popover.getByText('This environment (pw)')).toBeVisible();
    await expect(popover.getByText('Upstream ERun')).toBeVisible();
    await expect(page.getByRole('option', { name: /1\.0\.16/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toBeVisible();
  });

  test("reopening the picker resets to the env's own suggestions, not a prior open's (#767)", async ({
    app,
    page,
    seededEnv,
  }) => {
    // Suggestions are dialog-owned and reset on open, so a reopen never shows a prior open's versions.
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
        await secondHeld;
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { suggestions: [], notices: [] } }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /2\.0\.0/ })).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await expect(page.getByRole('option', { name: /2\.0\.0/ })).toHaveCount(0);

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
    // Environments-changed deltas must not touch the dialog-owned suggestions.
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

    await expect(page.getByRole('option', { name: /1\.0\.16/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /9\.9\.9/ })).toHaveCount(0);
  });

  test('saving a container registry re-queries the picker so the new registry contributes (#995)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A save must re-query the picker, or a newly added registry looks unusable.
    let calls = 0;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        calls += 1;
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

    // Deploy-only: a second build registry would be invalid.
    await app.manageDialog.selectTab('General');
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await page.keyboard.press('Escape');
    await app.manageDialog.registryRoleCheckbox(1, 'build').click();
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

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
    await expect(page.getByRole('option', { name: /1\.0\.0/ })).toBeVisible();
  });

  test('deploying a version sends its runtime image so the deploy targets that erun-devops version (#792)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // The deploy's runtime image must resolve from the dialog-owned list; falling
    // back to the shared slice would silently drop --runtime-image.
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
    // Editing the field clears the picked image.
    await app.manageDialog.runtimeVersionInput().fill('1.0.134');
    await expect(app.manageDialog.deployButton()).toBeEnabled();

    const deployRequest = page.waitForRequest(
      (req) =>
        req.url().includes('/__erun_invoke') &&
        (req.postData() ?? '').includes('StartDeploySession'),
    );
    await app.manageDialog.deploy();
    const payload = (await deployRequest).postData() ?? '';
    expect(payload).toContain('ghcr.io/sophium/erun-devops');
  });
});

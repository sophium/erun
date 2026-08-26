import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The Ports tab's public-exposure surface (issue #1351). The headless harness
// has no real cluster and no project with a platform block (see
// fixtures/seedRoot.ts's kubectl stub and AGENTS.md's "Isolated config root"
// section), so:
// - the "not applicable" state is exercised for real, against the seeded
//   env's actual (unconfigured) project -- no stub needed;
// - every other state (configured, populated, restricted, a genuine listing
//   failure, in-flight create/remove) is staged by stubbing the three RPCs
//   over /__erun_invoke, exactly as manage-environment-health.spec.ts and
//   manage-delete-partial-failure.spec.ts already do for their own surfaces.

type RpcHandler = () => Record<string, unknown> | Promise<Record<string, unknown>>;

async function stubExposureRpcs(page: Page, handlers: Record<string, RpcHandler>): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    const handler = handlers[parsed.method];
    if (handler) {
      const body = await handler();
      return route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
    }
    await route.continue();
  });
}

const CONFIGURED_EMPTY = { data: { configured: true, restricted: false, services: [] } };
const RESTRICTED = { data: { configured: true, restricted: true, services: [] } };
const LOAD_FAILURE = {
  data: {
    configured: true,
    restricted: false,
    error: 'EXPOSURE_LOAD_FAILURE_MARKER',
    services: [],
  },
};
const POPULATED = {
  data: {
    configured: true,
    restricted: false,
    services: [{ service: 'api', hostname: 'api.pw-alpha.services.test', scheme: 'https' }],
  },
};

test.describe('manage dialog ports tab — public exposures (#1351)', () => {
  test('a non-hosted environment reports its public address as not applicable, not an empty list', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Not available for this environment')).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: /Expose a service/ })).toHaveCount(0);

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-not-applicable.png',
    });

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a restricted listing is distinct from an empty one', async ({ app, page }) => {
    await stubExposureRpcs(page, { ListEnvironmentExposures: () => RESTRICTED });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('You may not have access to see this')).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1351-visual/ports-restricted.png' });

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a genuine listing failure offers a retry, and retrying re-fetches', async ({
    app,
    page,
  }) => {
    let calls = 0;
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        calls++;
        return calls === 1 ? LOAD_FAILURE : CONFIGURED_EMPTY;
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('EXPOSURE_LOAD_FAILURE_MARKER')).toBeVisible();
    const retry = dialog.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible();

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1351-visual/ports-failed.png' });

    await retry.click();
    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    expect(calls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('exposing a service shows an in-flight state and renders the copyable, openable result', async ({
    app,
    page,
  }) => {
    let exposeCalls = 0;
    let listCalls = 0;
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return listCalls === 1 ? CONFIGURED_EMPTY : POPULATED;
      },
      ExposeEnvironmentService: async () => {
        exposeCalls++;
        // A real expose round-trips DNS + an Ingress apply; hold the response
        // open briefly so the in-flight state is actually observable rather
        // than resolving before the assertion below can catch it.
        await new Promise((resolve) => setTimeout(resolve, 300));
        return {
          data: { service: 'api', hostname: 'api.pw-alpha.services.test', scheme: 'https' },
        };
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();

    await dialog.locator('#expose-service-name').fill('api');
    await dialog.locator('#expose-target-ip').fill('203.0.113.10');
    const submit = dialog.getByRole('button', { name: /Expose a service/ });
    await submit.click();

    await expect(dialog.getByRole('button', { name: 'Exposing...' })).toBeVisible();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-create-inflight.png',
    });

    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    expect(exposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1351-visual/ports-populated.png' });

    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    await dialog.getByRole('button', { name: /Copy the address for api/ }).click();
    const copyReq = await clipboardWrite;
    expect(copyReq.postData() ?? '').toContain('https://api.pw-alpha.services.test');

    const popupPromise = page.context().waitForEvent('page');
    await dialog.getByRole('button', { name: /Open the address for api/ }).click();
    const popup = await popupPromise;
    expect(popup.url()).toBe('https://api.pw-alpha.services.test/');
    await popup.close();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('exposing without a target IP fails clearly instead of silently doing nothing', async ({
    app,
    page,
  }) => {
    await stubExposureRpcs(page, { ListEnvironmentExposures: () => CONFIGURED_EMPTY });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    const submit = dialog.getByRole('button', { name: /Expose a service/ });
    await expect(submit).toBeDisabled();

    await dialog.locator('#expose-service-name').fill('api');
    await expect(submit).toBeDisabled();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('removing public access is a two-step confirm and shows an in-flight state', async ({
    app,
    page,
  }) => {
    let unexposeCalls = 0;
    let listCalls = 0;
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return listCalls === 1 ? POPULATED : CONFIGURED_EMPTY;
      },
      UnexposeEnvironment: async () => {
        unexposeCalls++;
        await new Promise((resolve) => setTimeout(resolve, 300));
        return { data: { wildcardName: '*.pw-alpha.services.test' } };
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    const removeButton = dialog.getByRole('button', { name: 'Remove public access' });
    await expect(removeButton).toBeVisible();
    await removeButton.click();

    // Step 1: a named warning, not an immediate delete.
    await expect(dialog.getByText(/removes the public address for every service/)).toBeVisible();
    const confirm = dialog.getByRole('button', { name: 'Confirm remove' });
    await expect(confirm).toBeVisible();
    expect(unexposeCalls).toBe(0);

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-remove-confirm.png',
    });

    // Step 2: the separate explicit action that actually commits it.
    await confirm.click();
    await expect(dialog.getByRole('button', { name: 'Removing...' })).toBeVisible();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-remove-inflight.png',
    });

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    expect(unexposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

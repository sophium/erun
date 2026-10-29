import type { Locator, Page } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import { expectFramesAllDistinct, holdResponse } from '../../../fixtures/visualFrames.js';
import type { AppShell } from '../../../pages/index.js';

// The harness has no cluster or platform block, so every state except "not
// applicable" is staged by stubbing the RPCs.

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
    services: [
      { service: 'api', hostname: 'api.pw-alpha.services.test', scheme: 'https', tlsReady: true },
    ],
  },
};
const PENDING_CERTIFICATE = {
  data: {
    configured: true,
    restricted: false,
    services: [
      {
        service: 'web',
        hostname: 'web.pw-alpha.services.test',
        scheme: 'https',
        tlsReady: false,
        tlsNotReadyReason: 'Issuing: waiting for order to complete',
      },
    ],
  },
};

// A round-trip failure: a computed { configured, error } never reaches the catch.
const SERVICES_READ_FAILURE = { error: 'SERVICES_READ_FAILURE_MARKER' };

const NO_SERVICES = { data: { configured: true, restricted: false, services: [] } };
const SERVICES_POPULATED = {
  data: {
    configured: true,
    restricted: false,
    services: [{ name: 'pw-web', type: 'ClusterIP', ports: [{ name: 'http', port: 8080 }] }],
  },
};

// expect's 10s default is independent of the test's budget, so wait on the resolved state first.
async function convergeOnPortsTab(app: AppShell, resolved: Locator): Promise<void> {
  await app.manageDialog.selectTab('Ports');
  await resolved.waitFor({ state: 'visible' });
}

test.describe('manage dialog ports tab — public exposures (#1351)', () => {
  test('a cluster-backed environment with no platform block names the fix and links to it', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('Not available for this environment'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Not available for this environment')).toBeVisible();
    await expect(dialog.getByText(/isn't set up for hosted deployment yet/)).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: /Expose a service/ })).toHaveCount(0);

    const docsLink = dialog.getByRole('button', { name: /View the platform: block reference/ });
    await expect(docsLink).toBeVisible();

    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-not-applicable.png'),
    });

    const [popup] = await Promise.all([app.page.waitForEvent('popup'), docsLink.click()]);
    // The browser adds a trailing slash before the fragment.
    await expect
      .poll(() => popup.url())
      .toBe('https://docs.erunpaas.com/reference/configuration/#platform-block');
    await popup.close();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a host environment reports its type as never exposable, with no dangling action', async ({
    app,
    seededHostEnv,
  }) => {
    const { tenant, environment } = seededHostEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('Not available for this environment type'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Not available for this environment type')).toBeVisible();
    await expect(dialog.getByText(/no pod and no cluster/)).toBeVisible();
    // A host env can never be exposed, so no action is offered.
    await expect(
      dialog.getByRole('button', { name: /View the platform: block reference/ }),
    ).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: /Expose a service/ })).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a restricted listing is distinct from an empty one', async ({ app, page }) => {
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => RESTRICTED,
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('You may not have access to see this'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('You may not have access to see this')).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);

    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-restricted.png'),
    });

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
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('EXPOSURE_LOAD_FAILURE_MARKER'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('EXPOSURE_LOAD_FAILURE_MARKER')).toBeVisible();
    const retry = dialog.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible();

    await dialog.screenshot({ path: artifactPath('test-results/1351-visual/ports-failed.png') });

    await retry.click();
    await dialog.getByText('Nothing exposed yet').waitFor({ state: 'visible' });
    expect(calls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Only a services-read failure renders the picker beside a resolved exposure list;
  // it must not blank that list.
  test('a failed services read leaves the exposure list’s resolved addresses intact', async ({
    app,
    page,
  }) => {
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => POPULATED,
      ListEnvironmentServices: () => SERVICES_READ_FAILURE,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('api.pw-alpha.services.test'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    await expect(dialog.getByText("Couldn't load public addresses")).toHaveCount(0);

    const pickerFailure = dialog.getByText(/Could not read this environment's services/);
    await expect(pickerFailure).toBeVisible();
    await expect(pickerFailure).toContainText('SERVICES_READ_FAILURE_MARKER');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // cert-manager writes the tls block before issuance, so scheme alone can't tell pending from ready.
  test('a service exposed under a still-issuing certificate reads as pending, not ready', async ({
    app,
    page,
  }) => {
    let listCalls = 0;
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return PENDING_CERTIFICATE;
      },
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByText('web.pw-alpha.services.test'),
    );
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('web.pw-alpha.services.test')).toBeVisible();
    await expect(
      dialog.getByText('Certificate pending: Issuing: waiting for order to complete'),
    ).toBeVisible();

    await dialog.screenshot({
      path: artifactPath('test-results/1918-visual/ports-cert-pending.png'),
    });

    const refresh = dialog.getByRole('button', { name: 'Refresh public addresses' });
    await expect(refresh).toBeVisible();
    expect(listCalls).toBe(1);
    await refresh.click();
    await expect.poll(() => listCalls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('exposing a service shows an in-flight state and renders the copyable, openable result', async ({
    app,
    page,
  }) => {
    let exposeCalls = 0;
    let listCalls = 0;
    const exposeGate = holdResponse();
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return listCalls === 1 ? CONFIGURED_EMPTY : POPULATED;
      },
      ExposeEnvironmentService: async () => {
        exposeCalls++;
        // Held until the in-flight frame is captured; a sleep would race the screenshot.
        await exposeGate.held;
        return {
          data: { service: 'api', hostname: 'api.pw-alpha.services.test', scheme: 'https' },
        };
      },
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(app, app.manageDialog.locator().getByText('Nothing exposed yet'));
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-empty-configured.png'),
    });

    await dialog.locator('#expose-service-name').fill('api');
    await dialog.locator('#expose-target-ip').fill('203.0.113.10');
    const submit = dialog.getByRole('button', { name: /Expose a service/ });
    await submit.click();

    await expect(dialog.getByRole('button', { name: 'Exposing...' })).toBeVisible();
    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-create-inflight.png'),
    });
    exposeGate.release();

    await dialog.getByText('api.pw-alpha.services.test').waitFor({ state: 'visible' });
    expect(exposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await dialog.screenshot({ path: artifactPath('test-results/1351-visual/ports-populated.png') });
    // A capture that settled early collapses into its settled sibling.
    await expectFramesAllDistinct([
      artifactPath('test-results/1351-visual/ports-empty-configured.png'),
      artifactPath('test-results/1351-visual/ports-create-inflight.png'),
      artifactPath('test-results/1351-visual/ports-populated.png'),
    ]);

    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    await dialog.getByRole('button', { name: /Copy the address for api/ }).click();
    const copyReq = await clipboardWrite;
    expect(copyReq.postData() ?? '').toContain('https://api.pw-alpha.services.test');

    // Fake domain without DNS; stub it so the popup doesn't land on chrome-error://.
    await page
      .context()
      .route('https://api.pw-alpha.services.test/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/html', body: '<html></html>' }),
      );
    const popupPromise = page.context().waitForEvent('page');
    await dialog.getByRole('button', { name: /Open the address for api/ }).click();
    const popup = await popupPromise;
    await popup.waitForLoadState();
    expect(popup.url()).toBe('https://api.pw-alpha.services.test/');
    await popup.close();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('exposing without a target IP fails clearly instead of silently doing nothing', async ({
    app,
    page,
  }) => {
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => CONFIGURED_EMPTY,
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(app, app.manageDialog.locator().getByText('Nothing exposed yet'));
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    const submit = dialog.getByRole('button', { name: /Expose a service/ });
    await expect(submit).toBeDisabled();

    await dialog.locator('#expose-service-name').fill('api');
    await expect(submit).toBeDisabled();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('picking a service fills the form and routes the expose call to its backend', async ({
    app,
    page,
  }) => {
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => CONFIGURED_EMPTY,
      ListEnvironmentServices: () => SERVICES_POPULATED,
      ExposeEnvironmentService: () => ({
        data: { service: 'web', hostname: 'web.pw-alpha.services.test', scheme: 'https' },
      }),
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(app, app.manageDialog.locator().getByText('Nothing exposed yet'));
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    const picker = dialog.getByRole('combobox', { name: 'Service' });
    await expect(picker).toBeVisible();
    await picker.click();
    await page.getByRole('option', { name: /pw-web/ }).click();

    // The tenant prefix is stripped for the label but kept for the backend Service name.
    await expect(dialog.locator('#expose-service-name')).toHaveValue('web');
    await expect(dialog.locator('#expose-port')).toHaveValue('8080');

    const exposeRequest = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_invoke') &&
        (req.postData() ?? '').includes('"method":"ExposeEnvironmentService"'),
    );
    await dialog.locator('#expose-target-ip').fill('203.0.113.10');
    await dialog.getByRole('button', { name: /Expose a service/ }).click();
    const req = await exposeRequest;
    expect(req.postData() ?? '').toContain('"backendService":"pw-web"');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('removing public access is a two-step confirm and shows an in-flight state', async ({
    app,
    page,
  }) => {
    let unexposeCalls = 0;
    let listCalls = 0;
    const unexposeGate = holdResponse();
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return listCalls === 1 ? POPULATED : CONFIGURED_EMPTY;
      },
      UnexposeEnvironment: async () => {
        unexposeCalls++;
        // Held until the in-flight frame is captured.
        await unexposeGate.held;
        return { data: { wildcardName: '*.pw-alpha.services.test' } };
      },
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await convergeOnPortsTab(
      app,
      app.manageDialog.locator().getByRole('button', { name: 'Remove public access' }),
    );
    const dialog = app.manageDialog.locator();

    const removeButton = dialog.getByRole('button', { name: 'Remove public access' });
    await expect(removeButton).toBeVisible();
    await removeButton.click();

    await expect(dialog.getByText(/removes the public address for every service/)).toBeVisible();
    const confirm = dialog.getByRole('button', { name: 'Confirm remove' });
    await expect(confirm).toBeVisible();
    expect(unexposeCalls).toBe(0);

    await confirm.scrollIntoViewIfNeeded();
    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-remove-confirm.png'),
    });

    // The dialog footer has its own Cancel, so this names its effect.
    const keepExposed = dialog.getByRole('button', { name: 'Keep exposed' });
    await expect(keepExposed).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();

    await keepExposed.click();
    await expect(dialog.getByRole('button', { name: 'Remove public access' })).toBeVisible();
    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    expect(unexposeCalls).toBe(0);

    await removeButton.click();
    await expect(confirm).toBeVisible();
    await confirm.scrollIntoViewIfNeeded();

    await confirm.click();
    await expect(dialog.getByRole('button', { name: 'Removing...' })).toBeVisible();
    await dialog.screenshot({
      path: artifactPath('test-results/1351-visual/ports-remove-inflight.png'),
    });
    unexposeGate.release();
    await expectFramesAllDistinct([
      artifactPath('test-results/1351-visual/ports-remove-confirm.png'),
      artifactPath('test-results/1351-visual/ports-remove-inflight.png'),
    ]);

    await dialog.getByText('Nothing exposed yet').waitFor({ state: 'visible' });
    expect(unexposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

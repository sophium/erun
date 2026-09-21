import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import { expectFramesAllDistinct, holdResponse } from '../../../fixtures/visualFrames.js';

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

// ListEnvironmentServices powers the picker rendered inside the same form
// (#1911) -- it shares UIExposureList's {configured, restricted, services}
// shape by design, so NO_SERVICES doubles as its "nothing running" stub.
// Every test below stubs it explicitly rather than falling through to the
// real backend call: that fallthrough is exactly what let #1911 ship the
// picker with these specs never actually exercising the paired read.
// A round-trip failure, not a computed one. refreshManageExposures only ever
// saw a pair rejection from a read whose RPC itself failed, so this stages the
// bridge's { error } envelope rather than LOAD_FAILURE's computed
// { configured, error } result: a computed failure resolves the promise, never
// reaches the catch that blanked both panels, and so cannot reproduce #1934.
const SERVICES_READ_FAILURE = { error: 'SERVICES_READ_FAILURE_MARKER' };

const NO_SERVICES = { data: { configured: true, restricted: false, services: [] } };
const SERVICES_POPULATED = {
  data: {
    configured: true,
    restricted: false,
    services: [{ name: 'pw-web', type: 'ClusterIP', ports: [{ name: 'http', port: 8080 }] }],
  },
};

test.describe('manage dialog ports tab — public exposures (#1351)', () => {
  test('a cluster-backed environment with no platform block names the fix and links to it', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Not available for this environment')).toBeVisible();
    await expect(dialog.getByText(/isn't set up for hosted deployment yet/)).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: /Expose a service/ })).toHaveCount(0);

    // The empty state must carry the action that resolves it, not just name
    // the condition (root AGENTS.md "Smooth, Seamless, No Dead Ends").
    const docsLink = dialog.getByRole('button', { name: /View the platform: block reference/ });
    await expect(docsLink).toBeVisible();

    await dialog.screenshot({
      path: 'test-results/1351-visual/ports-not-applicable.png',
    });

    const [popup] = await Promise.all([app.page.waitForEvent('popup'), docsLink.click()]);
    // The browser normalizes a bare path to add a trailing slash before the
    // fragment (mirrors sidebar-documentation-link.spec.ts's own '/' suffix).
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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Not available for this environment type')).toBeVisible();
    await expect(dialog.getByText(/no pod and no cluster/)).toBeVisible();
    // Unlike the fixable "no platform block" case, there is nothing to fix
    // here -- a host env can never be exposed, so no action is offered.
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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('You may not have access to see this')).toBeVisible();
    await expect(dialog.getByText('Nothing exposed yet')).toHaveCount(0);

    await dialog.screenshot({ path: 'test-results/1351-visual/ports-restricted.png' });

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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('EXPOSURE_LOAD_FAILURE_MARKER')).toBeVisible();
    const retry = dialog.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible();

    await dialog.screenshot({ path: 'test-results/1351-visual/ports-failed.png' });

    await retry.click();
    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    expect(calls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // #1934's defect, in the only direction the DOM can show it. The two reads
  // are paired, so a rejection from either used to land in
  // refreshManageExposures' one catch and overwrite *both* panels with that
  // error -- the exposure list included, discarding a listing that had already
  // resolved. Every other case here stubs both reads to the same class of
  // outcome (both succeed, or the failed one is the exposures read), so a list
  // blanked by the picker's failure was indistinguishable from one that simply
  // had nothing to show.
  //
  // The failure has to come from the services read specifically: when the
  // *exposures* read fails, ExposuresBody returns its error state before
  // reaching ExposeServiceForm, so no picker renders at all and the two states
  // are observationally identical.
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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    // The read that answered still renders what it answered ...
    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    // ... and is not wearing the failure of the read that did not.
    await expect(dialog.getByText("Couldn't load public addresses")).toHaveCount(0);

    // The picker reports its own failure, so the form still names the way out
    // rather than silently losing its options.
    const pickerFailure = dialog.getByText(/Could not read this environment's services/);
    await expect(pickerFailure).toBeVisible();
    await expect(pickerFailure).toContainText('SERVICES_READ_FAILURE_MARKER');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A certificate that has not issued yet is a distinct state from "exposed
  // and working": the Ingress already carries a tls block -- cert-manager
  // writes that before issuance completes -- so scheme alone would otherwise
  // render this row identically to a service that is actually safe to open.
  // Also exercises the manual refresh affordance a developer needs while
  // waiting for the certificate to issue.
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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('web.pw-alpha.services.test')).toBeVisible();
    await expect(
      dialog.getByText('Certificate pending: Issuing: waiting for order to complete'),
    ).toBeVisible();

    await dialog.screenshot({
      path: 'test-results/1918-visual/ports-cert-pending.png',
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
        // A real expose round-trips DNS + an Ingress apply. Hold the response
        // open until this spec has captured the in-flight frame below, rather
        // than for a fixed interval: a sleep races the screenshot, and the
        // capture that loses writes the settled render into the in-flight
        // frame, making it byte-identical to ports-populated.png.
        await exposeGate.held;
        return {
          data: { service: 'api', hostname: 'api.pw-alpha.services.test', scheme: 'https' },
        };
      },
      ListEnvironmentServices: () => NO_SERVICES,
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    await dialog.screenshot({
      path: 'test-results/1351-visual/ports-empty-configured.png',
    });

    await dialog.locator('#expose-service-name').fill('api');
    await dialog.locator('#expose-target-ip').fill('203.0.113.10');
    const submit = dialog.getByRole('button', { name: /Expose a service/ });
    await submit.click();

    await expect(dialog.getByRole('button', { name: 'Exposing...' })).toBeVisible();
    await dialog.screenshot({
      path: 'test-results/1351-visual/ports-create-inflight.png',
    });
    // Only now let the stubbed call settle: the frame above is the evidence
    // that the pending state renders, so it must be on disk before the
    // response can reach the renderer.
    exposeGate.release();

    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    expect(exposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await dialog.screenshot({ path: 'test-results/1351-visual/ports-populated.png' });
    // Every frame this test wrote must be a distinct file: a capture that
    // settles early collapses into its settled sibling, and the bundle then
    // documents a state the run never saw.
    await expectFramesAllDistinct([
      'test-results/1351-visual/ports-empty-configured.png',
      'test-results/1351-visual/ports-create-inflight.png',
      'test-results/1351-visual/ports-populated.png',
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

    // The hostname is a fake test domain with no real DNS, so stub it rather
    // than let the popup hit the network and land on chrome-error:// --
    // mirrors diagnostics-orchestrator-context.spec.ts's github.com stub for
    // the same reason.
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

  // The picker (#1911) turns "type a Service name you already know" into
  // "pick one of what's actually running", filling in the label (tenant
  // prefix stripped) and the sole port, and threading the real Service name
  // through as the Ingress backend rather than the <tenant>-<label>
  // derivation -- see ManageDialogPortsServicePicker.tsx and
  // exposeServicePickController.ts.
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
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    const picker = dialog.getByRole('combobox', { name: 'Service' });
    await expect(picker).toBeVisible();
    await picker.click();
    await page.getByRole('option', { name: /pw-web/ }).click();

    // The tenant prefix ('pw-') is stripped for the label but kept for the
    // backend Service name -- that split is the whole point of the picker.
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
        // Same gate as the expose path: the removal round-trip a real cluster
        // performs is held open until this spec has captured the in-flight
        // frame, so the capture cannot land after the state has settled.
        await unexposeGate.held;
        return { data: { wildcardName: '*.pw-alpha.services.test' } };
      },
      ListEnvironmentServices: () => NO_SERVICES,
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

    await confirm.scrollIntoViewIfNeeded();
    await dialog.screenshot({
      path: 'test-results/1351-visual/ports-remove-confirm.png',
    });

    // The negative names its effect rather than reusing the generic word: the
    // dialog's own footer also renders a Cancel (opposite Save) that closes the
    // dialog and discards unsaved edits, so two buttons reading "Cancel" a few
    // pixels apart would do different things. Matches ManageDialogJobCancel's
    // "Keep running".
    const keepExposed = dialog.getByRole('button', { name: 'Keep exposed' });
    await expect(keepExposed).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();

    // Dismissing keeps every address and issues no write -- the behaviour the
    // label now names.
    await keepExposed.click();
    await expect(dialog.getByRole('button', { name: 'Remove public access' })).toBeVisible();
    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    expect(unexposeCalls).toBe(0);

    // Re-open the confirm so the committing half of the flow below is unchanged.
    await removeButton.click();
    await expect(confirm).toBeVisible();
    await confirm.scrollIntoViewIfNeeded();

    // Step 2: the separate explicit action that actually commits it.
    await confirm.click();
    await expect(dialog.getByRole('button', { name: 'Removing...' })).toBeVisible();
    await dialog.screenshot({
      path: 'test-results/1351-visual/ports-remove-inflight.png',
    });
    unexposeGate.release();
    await expectFramesAllDistinct([
      'test-results/1351-visual/ports-remove-confirm.png',
      'test-results/1351-visual/ports-remove-inflight.png',
    ]);

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    expect(unexposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

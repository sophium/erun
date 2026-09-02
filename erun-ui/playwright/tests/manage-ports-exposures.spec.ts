import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The Ports tab's public-exposure surface (issue #1351; the service-picker
// redesign discovering real Services is issue #1906). The headless harness
// has no real cluster and no project with a platform block (see
// fixtures/seedRoot.ts's kubectl stub and AGENTS.md's "Isolated config root"
// section), so:
// - the "not applicable" state is exercised for real, against the seeded
//   env's actual (unconfigured) project -- no stub needed;
// - every other state (configured, populated, restricted, a genuine listing
//   failure, in-flight create/remove) is staged by stubbing the RPCs over
//   /__erun_invoke, exactly as manage-environment-health.spec.ts and
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
// pw-api hasn't been exposed yet but follows the tenant's naming convention
// ("pw-" stripped leaves the label "api"); pw-legacy-backend hasn't either,
// but its name carries no such prefix -- erun expose has no way to route to
// it, so the picker must offer it as visibly not exposable, never silently.
const NOT_EXPOSED_CANDIDATES = {
  data: {
    configured: true,
    restricted: false,
    services: [
      { name: 'pw-api', ports: [{ port: 80 }], exposed: false, exposableLabel: 'api' },
      { name: 'legacy-backend', ports: [{ port: 3000 }], exposed: false },
    ],
  },
};
const POPULATED = {
  data: {
    configured: true,
    restricted: false,
    services: [
      {
        name: 'pw-api',
        ports: [{ port: 80 }],
        exposed: true,
        hostname: 'api.pw-alpha.services.test',
        scheme: 'https',
      },
    ],
  },
};

test.describe('manage dialog ports tab — public exposures (#1351, #1906)', () => {
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
    await expect(dialog.getByRole('combobox', { name: 'Service' })).toHaveCount(0);

    // The empty state must carry the action that resolves it, not just name
    // the condition (root AGENTS.md "Smooth, Seamless, No Dead Ends").
    const docsLink = dialog.getByRole('button', { name: /View the platform: block reference/ });
    await expect(docsLink).toBeVisible();

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-not-applicable.png',
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
    await expect(dialog.getByRole('combobox', { name: 'Service' })).toHaveCount(0);

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
    await expect(dialog.getByText('No Services in this environment yet')).toBeVisible();
    expect(calls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('the picker offers a real Service, previews its hostname, and exposing it renders the copyable, openable result', async ({
    app,
    page,
  }) => {
    let exposeCalls = 0;
    let listCalls = 0;
    let previewCalls = 0;
    await stubExposureRpcs(page, {
      ListEnvironmentExposures: () => {
        listCalls++;
        return listCalls === 1 ? NOT_EXPOSED_CANDIDATES : POPULATED;
      },
      PreviewExposeEnvironmentService: () => {
        previewCalls++;
        return {
          data: {
            hostname: 'api.pw-alpha.services.test',
            scheme: 'https',
            tlsEnabled: true,
          },
        };
      },
      ExposeEnvironmentService: async () => {
        exposeCalls++;
        // A real expose round-trips DNS + an Ingress apply; hold the response
        // open briefly so the in-flight state is actually observable rather
        // than resolving before the assertion below can catch it.
        await new Promise((resolve) => setTimeout(resolve, 300));
        return { data: { hostname: 'api.pw-alpha.services.test', scheme: 'https' } };
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    await expect(dialog.getByText('Nothing exposed yet')).toBeVisible();
    const picker = dialog.getByRole('combobox', { name: 'Service' });
    await expect(picker).toBeVisible();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1906-visual/ports-picker-empty.png',
    });

    // A Service whose name carries no tenant prefix is offered, visibly, as
    // not exposable -- never hidden, never silently broken (issue #1906's
    // "load-bearing question": a caller must not offer an action that would
    // 503).
    await picker.click();
    const blockedOption = page.getByRole('option', { name: /legacy-backend.*not exposable yet/ });
    await expect(blockedOption).toBeVisible();
    await expect(blockedOption).toHaveAttribute('aria-disabled', 'true');
    await page.keyboard.press('Escape');

    await picker.click();
    await page.getByRole('option', { name: 'pw-api (80)' }).click();
    await expect(dialog.getByText(/doesn't carry this tenant's naming prefix/)).toHaveCount(0);

    await dialog.locator('#expose-target-ip').fill('203.0.113.10');
    await expect(dialog.getByText('api.pw-alpha.services.test')).toBeVisible();
    expect(previewCalls).toBeGreaterThan(0);
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1906-visual/ports-preview.png',
    });

    const listCallsBeforeSubmit = listCalls;
    const submit = dialog.getByRole('button', { name: 'Expose this service' });
    await submit.click();

    await expect(dialog.getByRole('button', { name: 'Exposing...' })).toBeVisible();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1906-visual/ports-create-inflight.png',
    });

    // The exposed row (not just its own hostname text, which the still-fading
    // preview panel could also carry momentarily) is the real completion
    // signal: it only exists once the list has actually been re-fetched.
    await expect(dialog.getByRole('button', { name: 'Copy the address for pw-api' })).toBeVisible();
    expect(exposeCalls).toBe(1);
    expect(listCalls).toBeGreaterThan(listCallsBeforeSubmit);

    await dialog.screenshot({ path: '/home/erun/.erun/outputs/1906-visual/ports-populated.png' });

    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    await dialog.getByRole('button', { name: /Copy the address for pw-api/ }).click();
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
    await dialog.getByRole('button', { name: /Open the address for pw-api/ }).click();
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
    await stubExposureRpcs(page, { ListEnvironmentExposures: () => NOT_EXPOSED_CANDIDATES });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');
    const dialog = app.manageDialog.locator();

    const submit = dialog.getByRole('button', { name: 'Expose this service' });
    await expect(submit).toBeDisabled();

    await dialog.getByRole('combobox', { name: 'Service' }).click();
    await page.getByRole('option', { name: 'pw-api (80)' }).click();
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

    await confirm.scrollIntoViewIfNeeded();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-remove-confirm.png',
    });

    // Step 2: the separate explicit action that actually commits it.
    await confirm.click();
    await expect(dialog.getByRole('button', { name: 'Removing...' })).toBeVisible();
    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/1351-visual/ports-remove-inflight.png',
    });

    await expect(dialog.getByText('No Services in this environment yet')).toBeVisible();
    expect(unexposeCalls).toBe(1);
    expect(listCalls).toBe(2);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

import type { Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';

function manyCloudContexts(count: number): Record<string, unknown>[] {
  return Array.from({ length: count }, (_, index) => ({
    name: `pw-ctx-${String(index)}`,
    provider: 'aws',
    cloudProviderAlias: 'me+aws@aws',
    region: 'eu-west-2',
    instanceType: 'c8gd.2xlarge',
    diskType: 'gp3',
    diskSizeGb: 100,
    kubernetesContext: `pw-ctx-${String(index)}`,
    status: 'running',
  }));
}

interface StubbedResponse {
  data?: Record<string, unknown> | unknown[];
  error?: string;
}

// stubRPC intercepts every named method on /__erun_invoke, returning
// whatever the caller's map holds for it — used below to exercise the cloud
// aliases empty state and the erun provider without depending on the seeded
// baseline's single AWS alias.
function stubRPC(
  page: import('@playwright/test').Page,
  responses: Record<string, StubbedResponse>,
): { calls: (method: string) => number } {
  const counts: Record<string, number> = {};
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    const stubbed = responses[body.method];
    if (stubbed) {
      counts[body.method] = (counts[body.method] ?? 0) + 1;
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(stubbed.error ? { error: stubbed.error } : { data: stubbed.data }),
      });
      return;
    }
    await route.continue();
  });
  return { calls: (method) => counts[method] ?? 0 };
}

test.describe('global config dialog', () => {
  test('opens and closes cleanly', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.locator()).toBeVisible();

    expect((await app.globalConfigDialog.getDefaultTenant()).trim()).toBe(SEED_TENANT);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('long cloud-provider value stays inside its column and does not cover the Region trigger', async ({
    app,
  }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    const provider = app.globalConfigDialog.cloudContextProviderTrigger();
    const region = app.globalConfigDialog.cloudContextRegionTrigger();
    await provider.waitFor({ state: 'visible' });
    await region.waitFor({ state: 'visible' });

    // Guards a regression where a long cloud-provider alias overflowed its
    // column and covered the Region trigger. The seeded alias is short, so
    // inject a long value to reproduce the overflow.
    await provider.evaluate((btn) => {
      const span = btn.querySelector('[data-slot="select-value"]');
      if (!(span instanceof HTMLElement)) {
        throw new Error('select-value span not found on Cloud provider trigger');
      }
      span.textContent = `long.user.name+0123456789@aws-${'x'.repeat(80)}`;
    });

    const providerBox = await boundingBoxOf(provider, 'Cloud provider trigger');
    const valueBox = await boundingBoxOf(
      app.globalConfigDialog.cloudContextProviderValue(),
      'Cloud provider value',
    );
    const regionBox = await boundingBoxOf(region, 'Region trigger');
    expect(valueBox.x + valueBox.width).toBeLessThanOrEqual(providerBox.x + providerBox.width);
    expect(providerBox.x + providerBox.width).toBeLessThanOrEqual(regionBox.x);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});

test.describe('global config dialog — cloud aliases add actions and erun provider', () => {
  test('empty state offers each add action exactly once', async ({ app, page }) => {
    // The dialog reads its aliases from the config it loads on open
    // (LoadERunConfig), not from LoadCloudProviderStatuses -- that one backs
    // the Refresh button alone. Stubbing the wrong call leaves the seeded
    // alias in place and the empty state never renders.
    stubRPC(page, {
      LoadERunConfig: {
        data: { defaultTenant: SEED_TENANT, cloudProviders: [], cloudContexts: [] },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // Anchor on the empty state first. Both surfaces render the same three
    // actions, so a count of 1 holds in either state -- without this the test
    // would pass even if the empty state never rendered, which is exactly how
    // it passed while stubbing the wrong call.
    await expect(app.globalConfigDialog.locator().getByText('No cloud aliases yet')).toBeVisible();

    // Regression guard for the four-buttons-two-actions defect: the header
    // and the empty state must never both render the same add action.
    await expect(app.globalConfigDialog.addAWSButton()).toHaveCount(1);
    await expect(app.globalConfigDialog.addCloudflareButton()).toHaveCount(1);
    await expect(app.globalConfigDialog.addERunButton()).toHaveCount(1);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('an erun alias groups under Hosted platforms once aliases exist', async ({ app, page }) => {
    stubRPC(page, {
      LoadERunConfig: {
        data: {
          defaultTenant: SEED_TENANT,
          cloudContexts: [],
          cloudProviders: [
            {
              alias: 'me+020362606330@aws',
              provider: 'aws',
              status: 'active',
              username: 'me',
              accountId: '020362606330',
            },
            {
              alias: 'erun+api.acme.test@erun',
              provider: 'erun',
              status: 'expired',
              username: 'erun',
              accountId: 'api.acme.test',
            },
          ],
        },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // With aliases present, the header (not the empty state) is the one
    // surface offering each add action.
    await expect(app.globalConfigDialog.addAWSButton()).toHaveCount(1);
    await expect(app.globalConfigDialog.addCloudflareButton()).toHaveCount(1);
    await expect(app.globalConfigDialog.addERunButton()).toHaveCount(1);

    await expect(app.globalConfigDialog.cloudAliasGroupHeading('erun')).toHaveText(
      'Hosted platforms',
    );
    await expect(app.globalConfigDialog.cloudAliasRow('erun+api.acme.test@erun')).toBeVisible();

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('connecting an erun platform from settings creates the alias and signs in', async ({
    app,
    page,
  }) => {
    const rpc = stubRPC(page, {
      LoadERunConfig: {
        data: { defaultTenant: SEED_TENANT, cloudProviders: [], cloudContexts: [] },
      },
      ConnectERunPlatform: {
        data: {
          alias: 'erun+api.acme.test@erun',
          provider: 'erun',
          status: 'expired',
          username: 'erun',
          accountId: 'api.acme.test',
        },
      },
      LoginCloudProvider: {
        data: {
          alias: 'erun+api.acme.test@erun',
          provider: 'erun',
          status: 'active',
          username: 'erun',
          accountId: 'api.acme.test',
        },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await app.globalConfigDialog.connectERunPlatform('https://api.acme.test');

    await expect(app.globalConfigDialog.cloudAliasRow('erun+api.acme.test@erun')).toBeVisible();
    // The settings entry point must reach the same connect-and-sign-in code
    // path as the tenant dashboard's Connect panel, not a second
    // implementation that creates the alias with no sign-in of its own.
    await expect.poll(() => rpc.calls('LoginCloudProvider')).toBe(1);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  // A connected erun alias row previously rendered only a static "Connected"
  // badge with no action at all -- once signed in there was no way back
  // through the UI (the CLI's `cloud logout` was the only path). These two
  // specs lock in the row's actual escape hatches, wired through
  // cloudApi.logoutCloudProvider / switchCloudProviderIdentity.
  const activeERunAlias = {
    alias: 'erun+api.acme.test@erun',
    provider: 'erun',
    status: 'active',
    username: 'erun',
    accountId: 'api.acme.test',
  };

  test('a connected erun alias can be signed out from its own row', async ({ app, page }) => {
    const rpc = stubRPC(page, {
      LoadERunConfig: {
        data: { defaultTenant: SEED_TENANT, cloudProviders: [activeERunAlias], cloudContexts: [] },
      },
      LogoutCloudProvider: {
        data: { ...activeERunAlias, status: 'not_configured' },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    const row = app.globalConfigDialog.cloudAliasRow(activeERunAlias.alias);
    await expect(row.getByText('Connected')).toBeVisible();
    await expect(
      app.globalConfigDialog.cloudAliasSwitchIdentityButton(activeERunAlias.alias),
    ).toBeVisible();

    await app.globalConfigDialog.logoutCloudAlias(activeERunAlias.alias);

    await expect.poll(() => rpc.calls('LogoutCloudProvider')).toBe(1);
    await expect(row.getByText('Connected')).toHaveCount(0);
    await expect(row.getByRole('button', { name: 'Login' })).toBeVisible();

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('signing in as someone else re-authenticates without logging out first', async ({
    app,
    page,
  }) => {
    const rpc = stubRPC(page, {
      LoadERunConfig: {
        data: { defaultTenant: SEED_TENANT, cloudProviders: [activeERunAlias], cloudContexts: [] },
      },
      SwitchCloudProviderIdentity: {
        data: { ...activeERunAlias, status: 'active' },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await app.globalConfigDialog.switchCloudAliasIdentity(activeERunAlias.alias);

    await expect.poll(() => rpc.calls('SwitchCloudProviderIdentity')).toBe(1);
    // Switching identity is a force re-login, not sign-out-then-sign-in: the
    // row never drops out of the connected state along the way.
    expect(rpc.calls('LogoutCloudProvider')).toBe(0);
    await expect(
      app.globalConfigDialog.cloudAliasRow(activeERunAlias.alias).getByText('Connected'),
    ).toBeVisible();

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});

test.describe('global config dialog — bounded height and scroll', () => {
  test('overflowing cloud contexts keep the title and footer reachable, and the body scrolls', async ({
    app,
    page,
  }) => {
    // 12 contexts is far more than fits in an 85vh frame at any viewport this
    // suite uses, so the body region must be what scrolls, not the dialog
    // growing past its cap -- the regression this guards.
    stubRPC(page, {
      LoadERunConfig: {
        data: {
          defaultTenant: SEED_TENANT,
          cloudProviders: [
            {
              alias: 'me+aws@aws',
              provider: 'aws',
              status: 'active',
              username: 'me',
              accountId: '111111111111',
            },
            {
              alias: 'me+cloudflare@cloudflare',
              provider: 'cloudflare',
              status: 'active',
              username: 'me',
            },
            {
              alias: 'erun+api.acme.test@erun',
              provider: 'erun',
              status: 'active',
              username: 'erun',
              accountId: 'api.acme.test',
            },
          ],
          cloudContexts: manyCloudContexts(12),
        },
      },
    });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // The dialog's own frame (DialogContent's max-h-[85vh]) caps the whole
    // surface regardless of how much is configured. The suite's config
    // viewport is 1440x1200 and this test never changes it.
    const dialog = app.globalConfigDialog.locator();
    const dialogBox = await boundingBoxOf(dialog, 'ERun settings dialog');
    expect(dialogBox.height).toBeLessThanOrEqual(1200 * 0.85 + 2);

    // The title and the footer buttons must both land inside that bounded
    // frame -- cut off above and cut off below is exactly the failure mode
    // being guarded against.
    const titleBox = await boundingBoxOf(
      dialog.getByText('ERun settings', { exact: true }),
      'ERun settings title',
    );
    expect(titleBox.y).toBeGreaterThanOrEqual(dialogBox.y - 1);
    const cancelBox = await boundingBoxOf(
      dialog.getByRole('button', { name: 'Cancel', exact: true }),
      'Cancel button',
    );
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    const saveBox = await boundingBoxOf(
      dialog.getByRole('button', { name: /Save settings|Saving/ }),
      'Save settings button',
    );
    expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);

    // The body region -- not the dialog itself -- is what scrolls.
    const bodyScroll = dialog.locator('.overflow-y-auto').first();
    const { scrollHeight, clientHeight } = await bodyScroll.evaluate((el) => ({
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    }));
    expect(scrollHeight).toBeGreaterThan(clientHeight);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('a short window keeps the title and footer buttons reachable without resizing', async ({
    app,
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 500 });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    const dialog = app.globalConfigDialog.locator();
    const dialogBox = await boundingBoxOf(dialog, 'ERun settings dialog');
    expect(dialogBox.height).toBeLessThanOrEqual(500 * 0.85 + 2);

    const titleBox = await boundingBoxOf(
      dialog.getByText('ERun settings', { exact: true }),
      'ERun settings title',
    );
    expect(titleBox.y).toBeGreaterThanOrEqual(0);
    const cancelBox = await boundingBoxOf(
      dialog.getByRole('button', { name: 'Cancel', exact: true }),
      'Cancel button',
    );
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(500);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();

    // Restore the config default viewport for later specs in the singleton backend.
    await page.setViewportSize({ width: 1440, height: 1200 });
  });
});

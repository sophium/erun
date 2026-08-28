import type { Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

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
      LoadERunConfig: { data: { defaultTenant: SEED_TENANT, cloudProviders: [], cloudContexts: [] } },
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
});

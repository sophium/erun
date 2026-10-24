import type { Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
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

// Far more contexts than fit in an 85vh frame, so the body must scroll.
function overflowingCloudConfig(): Record<string, unknown> {
  return {
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
  };
}

function requireFrame<T>(member: T | null, label: string): T {
  if (member === null) {
    throw new Error(`${label} is missing from the settings dialog frame`);
  }
  return member;
}

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

    // The seeded alias is short, so inject a long one to reproduce the overflow.
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

  // The config is held just past expect's 10s default, which test.setTimeout does
  // not move, so reads must carry the test's budget explicitly.
  test('a config that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    let holdUntil = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method !== 'LoadERunConfig') {
        await route.continue();
        return;
      }
      // Anchored to the request, so the delay is independent of dialog open time.
      if (holdUntil === 0) {
        holdUntil = Date.now() + 12_000;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: { defaultTenant: SEED_TENANT, cloudProviders: [], cloudContexts: [] },
        }),
      });
    });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await expect(app.globalConfigDialog.addAWSButton()).toHaveCount(1, withTestBudget());
    await expect(app.globalConfigDialog.addCloudflareButton()).toHaveCount(1, withTestBudget());

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});

test.describe('global config dialog — cloud aliases add actions and erun provider', () => {
  test('empty state offers each add action exactly once', async ({ app, page }) => {
    // The dialog reads aliases from LoadERunConfig; LoadCloudProviderStatuses only
    // backs Refresh.
    stubRPC(page, {
      LoadERunConfig: {
        data: { defaultTenant: SEED_TENANT, cloudProviders: [], cloudContexts: [] },
      },
    });
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // Anchor on the empty state first: both surfaces render the same actions, so
    // the counts below hold either way.
    await expect(app.globalConfigDialog.locator().getByText('No cloud aliases yet')).toBeVisible(
      withTestBudget(),
    );

    // The header and the empty state must never both render the add action.
    await expect(app.globalConfigDialog.addAWSButton()).toHaveCount(1, withTestBudget());
    await expect(app.globalConfigDialog.addCloudflareButton()).toHaveCount(1, withTestBudget());
    await expect(app.globalConfigDialog.addERunButton()).toHaveCount(1, withTestBudget());

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

    await expect(app.globalConfigDialog.addAWSButton()).toHaveCount(1, withTestBudget());
    await expect(app.globalConfigDialog.addCloudflareButton()).toHaveCount(1, withTestBudget());
    await expect(app.globalConfigDialog.addERunButton()).toHaveCount(1, withTestBudget());

    await expect(app.globalConfigDialog.cloudAliasGroupHeading('erun')).toHaveText(
      'Hosted platforms',
      withTestBudget(),
    );
    await expect(app.globalConfigDialog.cloudAliasRow('erun+api.acme.test@erun')).toBeVisible(
      withTestBudget(),
    );

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
    // Must reach the same connect-and-sign-in path as the tenant dashboard.
    await expect.poll(() => rpc.calls('LoginCloudProvider')).toBe(1);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

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
    await expect(row.getByText('Connected')).toBeVisible(withTestBudget());
    await expect(
      app.globalConfigDialog.cloudAliasSwitchIdentityButton(activeERunAlias.alias),
    ).toBeVisible();

    await app.globalConfigDialog.logoutCloudAlias(activeERunAlias.alias);

    await expect.poll(() => rpc.calls('LogoutCloudProvider')).toBe(1);
    await expect(row.getByText('Connected')).toHaveCount(0, withTestBudget());
    await expect(row.getByRole('button', { name: 'Login' })).toBeVisible(withTestBudget());

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
    // Switching identity is a force re-login, not sign-out-then-sign-in.
    expect(rpc.calls('LogoutCloudProvider')).toBe(0);
    await expect(
      app.globalConfigDialog.cloudAliasRow(activeERunAlias.alias).getByText('Connected'),
    ).toBeVisible(withTestBudget());

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});

test.describe('global config dialog — bounded height and scroll', () => {
  test('overflowing cloud contexts keep the title and footer reachable, and the body scrolls', async ({
    app,
    page,
  }) => {
    stubRPC(page, { LoadERunConfig: { data: overflowingCloudConfig() } });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // waitForOpen resolves on the loading shell; the assertions are about the loaded card.
    await app.globalConfigDialog.waitForLoadedFrame();

    // One layout snapshot: separate reads can straddle a transition.
    const frame = await app.globalConfigDialog.frameGeometry();

    const dialogBox = requireFrame(frame.dialog, 'ERun settings dialog');
    expect(dialogBox.height).toBeLessThanOrEqual(1200 * 0.85 + 2);

    const titleBox = requireFrame(frame.title, 'ERun settings title');
    expect(titleBox.y).toBeGreaterThanOrEqual(dialogBox.y - 1);
    const cancelBox = requireFrame(frame.cancel, 'Cancel button');
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    const saveBox = requireFrame(frame.save, 'Save settings button');
    expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);

    const body = requireFrame(frame.body, 'the settings dialog body scroller');
    expect(body.scrollHeight).toBeGreaterThan(body.clientHeight);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  // The config route is parked so the loading shell is provably on screen for
  // the first read; per-box reads across the load measured inconsistent layouts.
  test('the frame and the footer inside it are read from one layout, not across the config load', async ({
    app,
    page,
  }) => {
    let releaseConfig!: () => void;
    const configHeld = new Promise<void>((resolve) => {
      releaseConfig = resolve;
    });
    let configArrived = 0;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method !== 'LoadERunConfig') {
        await route.continue();
        return;
      }
      configArrived++;
      await configHeld;
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: overflowingCloudConfig() }),
      });
    });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // The shell has nothing to scroll, so measuring it would pass vacuously.
    await expect.poll(() => configArrived).toBe(1);
    const whileLoading = await app.globalConfigDialog.frameGeometry();
    const shellBody = requireFrame(whileLoading.body, 'the loading shell body scroller');
    expect(shellBody.scrollHeight).toBe(shellBody.clientHeight);

    releaseConfig();
    await app.globalConfigDialog.waitForLoadedFrame();
    const loaded = await app.globalConfigDialog.frameGeometry();

    for (const [label, frame] of [
      ['the loading shell', whileLoading],
      ['the loaded frame', loaded],
    ] as const) {
      const dialogBox = requireFrame(frame.dialog, `${label}: ERun settings dialog`);
      const saveBox = requireFrame(frame.save, `${label}: Save settings button`);
      expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    }

    const shellBox = requireFrame(whileLoading.dialog, 'the loading shell dialog');
    const dialogBox = requireFrame(loaded.dialog, 'the loaded dialog');
    expect(dialogBox.height).toBeGreaterThan(shellBox.height);
    expect(requireFrame(loaded.title, 'ERun settings title').y).toBeGreaterThanOrEqual(
      dialogBox.y - 1,
    );
    const cancelBox = requireFrame(loaded.cancel, 'Cancel button');
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    const body = requireFrame(loaded.body, 'the loaded frame body scroller');
    expect(body.scrollHeight).toBeGreaterThan(body.clientHeight);

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

    // At a 500px viewport the shell's frame would trivially satisfy these bounds.
    await app.globalConfigDialog.waitForLoadedFrame();
    const frame = await app.globalConfigDialog.frameGeometry();
    const dialogBox = requireFrame(frame.dialog, 'ERun settings dialog');
    expect(dialogBox.height).toBeLessThanOrEqual(500 * 0.85 + 2);

    const titleBox = requireFrame(frame.title, 'ERun settings title');
    expect(titleBox.y).toBeGreaterThanOrEqual(0);
    const cancelBox = requireFrame(frame.cancel, 'Cancel button');
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(500);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();

    // Restore the default viewport for later specs on the shared backend.
    await page.setViewportSize({ width: 1440, height: 1200 });
  });
});

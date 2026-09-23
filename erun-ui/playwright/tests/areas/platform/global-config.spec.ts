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

// 12 contexts is far more than fits in an 85vh frame at any viewport this
// suite uses, so the body region must be what scrolls, not the dialog growing
// past its cap. Used by both the bounded-height case and the case that pins the
// frame it is measured against.
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

// A frame member that is absent is a missing element, not a skipped assertion;
// this fails the spec by name the way boundingBoxOf does. A helper rather than
// an inline branch, which is what keeps the missing case out of the test body.
function requireFrame<T>(member: T | null, label: string): T {
  if (member === null) {
    throw new Error(`${label} is missing from the settings dialog frame`);
  }
  return member;
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
    stubRPC(page, { LoadERunConfig: { data: overflowingCloudConfig() } });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // Converge on the loaded frame before measuring anything inside it.
    // `waitForOpen` resolves on the ~195px `configLoading` shell, and every
    // assertion below is about the 85vh-capped card the config produces; the
    // geometry of one is not the geometry of the other (see the case below).
    await app.globalConfigDialog.waitForLoadedFrame();

    // One layout snapshot for the frame and everything asserted inside it.
    // Read as separate round trips these are separate layout moments, and a
    // transition landing between two of them is what reddened the gate: the
    // frame from one state and the footer from the next.
    const frame = await app.globalConfigDialog.frameGeometry();

    // The dialog's own frame (DialogContent's max-h-[85vh]) caps the whole
    // surface regardless of how much is configured. The suite's config
    // viewport is 1440x1200 and this test never changes it.
    const dialogBox = requireFrame(frame.dialog, 'ERun settings dialog');
    expect(dialogBox.height).toBeLessThanOrEqual(1200 * 0.85 + 2);

    // The title and the footer buttons must both land inside that bounded
    // frame -- cut off above and cut off below is exactly the failure mode
    // being guarded against.
    const titleBox = requireFrame(frame.title, 'ERun settings title');
    expect(titleBox.y).toBeGreaterThanOrEqual(dialogBox.y - 1);
    const cancelBox = requireFrame(frame.cancel, 'Cancel button');
    expect(cancelBox.y + cancelBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    const saveBox = requireFrame(frame.save, 'Save settings button');
    expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);

    // The body region -- not the dialog itself -- is what scrolls.
    const body = requireFrame(frame.body, 'the settings dialog body scroller');
    expect(body.scrollHeight).toBeGreaterThan(body.clientHeight);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  // The gate's own numbers. Its red measured this dialog's frame at 702.19 --
  // the bottom edge of the `configLoading` shell -- and the Save button at 1085,
  // the loaded frame's footer: 382px apart, on one dialog in one open state.
  // Neither measurement is wrong for the layout it was taken in. What separates
  // them is the config load, and on the gate it landed between two of the spec's
  // five reads. The landing point is decided by load, so this decides it
  // instead: the route handler parks on a promise the test releases, which makes
  // the shell provably the layout on screen for as long as it holds.
  //
  // The two reads below sit where the gate's own two landed: the frame while
  // the shell is up, the footer once the load has cleared it. Read as per-box
  // round trips at those same sites -- which is what the pre-fix case did, and
  // what this case pins against -- the pair is the one the gate measured 382px
  // apart: Expected <= 699.29, Received 1085.
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

    // Read 1, at the site the gate's frame read landed: the route is parked, so
    // this is the loading shell and nothing else can be. Its body has nothing
    // to scroll, which is what makes measuring it a vacuous pass rather than a
    // failure for every assertion the case above makes.
    await expect.poll(() => configArrived).toBe(1);
    const whileLoading = await app.globalConfigDialog.frameGeometry();
    const shellBody = requireFrame(whileLoading.body, 'the loading shell body scroller');
    expect(shellBody.scrollHeight).toBe(shellBody.clientHeight);

    // Read 2, at the site the gate's Save-button read landed. Converging on the
    // frame the load produces is what keeps this read off whatever happens to be
    // on screen when its round trip arrives -- and it is the read the loaded
    // assertions below are about.
    releaseConfig();
    await app.globalConfigDialog.waitForLoadedFrame();
    const loaded = await app.globalConfigDialog.frameGeometry();

    // Each read is one layout, so each frame contains its own footer. Split back
    // into per-box reads, these are the two boxes the gate measured 382px apart.
    for (const [label, frame] of [
      ['the loading shell', whileLoading],
      ['the loaded frame', loaded],
    ] as const) {
      const dialogBox = requireFrame(frame.dialog, `${label}: ERun settings dialog`);
      const saveBox = requireFrame(frame.save, `${label}: Save settings button`);
      expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(dialogBox.y + dialogBox.height + 1);
    }

    // ... and the loaded one is the frame the case above is about: capped,
    // titled, and scrolled.
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

    // Same convergence and one-snapshot read as the case above: a 500px
    // viewport makes the shell's 195px frame *look* like it satisfies every
    // bound here, so measuring it is how this case passes while asserting
    // nothing.
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

    // Restore the config default viewport for later specs in the singleton backend.
    await page.setViewportSize({ width: 1440, height: 1200 });
  });
});

import { test, expect } from '../../../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_CLOUDFLARE_ALIAS,
  SEED_ENV_GAMMA,
  SEED_TENANT,
} from '../../../fixtures/seedRoot.js';

// Add-alias is delegated to the CLI for every provider type — there is no
// in-app add form — and the harness cannot drive the interactive
// `erun cloud init` PTY (the seeded stub is inert for it), so the invariant
// these specs lock is only the observable one: the add button closes the
// settings dialog and no in-app add form ever renders. The guided flow itself
// is owned and tested by the CLI.
//
// The provider-correct session-exit toast is NOT asserted here: it fires only
// after the full PTY spawn → exit → render lifecycle, too flaky under the
// shared-backend harness. That exit-reason logic is a pure function verified
// live in the desktop app.

test.describe('multi-provider cloud aliases', () => {
  test('settings provider picker delegates both add flows to the CLI', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await expect(app.globalConfigDialog.cloudAliasGroupHeading('aws')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasGroupHeading('cloudflare')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUDFLARE_ALIAS)).toBeVisible();

    await expect(app.globalConfigDialog.addAWSButton()).toBeVisible();
    await expect(app.globalConfigDialog.addCloudflareButton()).toBeVisible();

    await app.globalConfigDialog.clickAddCloudflare();
    await app.globalConfigDialog.waitForClosed();
    await expect(app.globalConfigDialog.cloudflareForm()).toHaveCount(0);
  });

  test('settings AWS add also delegates to the CLI', async ({ app }) => {
    // Asserting AWS the same way as Cloudflare is the cross-provider
    // consistency invariant (Nielsen #4).
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.clickAddAWS();
    await expect(app.globalConfigDialog.locator()).toBeHidden();
  });

  test('sidebar shows one independent login row per provider type', async ({ app }) => {
    // gamma is the seeded env that attaches both an AWS and a Cloudflare alias.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_GAMMA);

    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUDFLARE_ALIAS)).toBeVisible();
    await expect.poll(() => app.sidebar.cloudAliasRowCount()).toBe(2);

    // Cloudflare has no OIDC, so its popover offers re-verify ("Verify token")
    // but no browser SSO ("Get bearer token").
    await app.sidebar.openCloudAliasPopover(SEED_CLOUDFLARE_ALIAS);
    await expect(app.sidebar.cloudAliasPopoverButton(/Verify token/)).toBeVisible();
    await expect(app.sidebar.cloudAliasPopoverButton(/Get bearer token/)).toHaveCount(0);
  });

  test('env manage dialog renders a selector per provider type', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_GAMMA);
    await app.manageDialog.waitForOpen();

    // gamma attaches both aliases.
    expect(await app.manageDialog.cloudAliasSlotVisible('aws')).toBe(true);
    expect(await app.manageDialog.cloudAliasSlotVisible('cloudflare')).toBe(true);

    // The old "Use host AWS credentials" checkbox is gone — attaching an alias
    // now delivers its credentials, so the alias selectors are the only control.
    await expect(app.manageDialog.hostAwsCredentialsCheckbox()).toHaveCount(0);
    await expect.poll(() => app.manageDialog.cloudAliasSlotValue('aws')).toBe(SEED_CLOUD_ALIAS);
    await expect
      .poll(() => app.manageDialog.cloudAliasSlotValue('cloudflare'))
      .toBe(SEED_CLOUDFLARE_ALIAS);

    // Per-type independence: clearing the Cloudflare slot must leave the AWS
    // slot attached. Cancel without saving so the shared seeded config stays
    // intact.
    await app.manageDialog.openCloudAliasSlotOptions('cloudflare');
    await app.manageDialog.cloudAliasNoneOption().click();
    await expect
      .poll(() => app.manageDialog.cloudAliasSlotValue('cloudflare'))
      .toBe('Select cloud alias');
    await expect.poll(() => app.manageDialog.cloudAliasSlotValue('aws')).toBe(SEED_CLOUD_ALIAS);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

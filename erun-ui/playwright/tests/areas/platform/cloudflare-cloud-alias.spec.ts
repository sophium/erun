import { test, expect } from '../../../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_CLOUDFLARE_ALIAS,
  SEED_ENV_GAMMA,
  SEED_TENANT,
} from '../../../fixtures/seedRoot.js';

// Add-alias is delegated to the CLI, whose PTY the harness cannot drive, so
// only "no in-app add form" is asserted.

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
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.clickAddAWS();
    await expect(app.globalConfigDialog.locator()).toBeHidden();
  });

  test('sidebar shows one independent login row per provider type', async ({ app }) => {
    // gamma attaches both an AWS and a Cloudflare alias.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_GAMMA);

    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUDFLARE_ALIAS)).toBeVisible();
    await expect.poll(() => app.sidebar.cloudAliasRowCount()).toBe(2);

    // Cloudflare has no OIDC, so no browser SSO.
    await app.sidebar.openCloudAliasPopover(SEED_CLOUDFLARE_ALIAS);
    await expect(app.sidebar.cloudAliasPopoverButton(/Verify token/)).toBeVisible();
    await expect(app.sidebar.cloudAliasPopoverButton(/Get bearer token/)).toHaveCount(0);
  });

  test('env manage dialog renders a selector per provider type', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_GAMMA);
    await app.manageDialog.waitForOpen();

    expect(await app.manageDialog.cloudAliasSlotVisible('aws')).toBe(true);
    expect(await app.manageDialog.cloudAliasSlotVisible('cloudflare')).toBe(true);

    await expect(app.manageDialog.hostAwsCredentialsCheckbox()).toHaveCount(0);
    await expect.poll(() => app.manageDialog.cloudAliasSlotValue('aws')).toBe(SEED_CLOUD_ALIAS);
    await expect
      .poll(() => app.manageDialog.cloudAliasSlotValue('cloudflare'))
      .toBe(SEED_CLOUDFLARE_ALIAS);

    // Cancel without saving so the shared seeded config stays intact.
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

import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_CLOUDFLARE_ALIAS,
  SEED_ENV_GAMMA,
  SEED_TENANT,
} from '../fixtures/seedRoot.js';

// Multi-provider cloud aliases (issue #630): the desktop surfaces AWS and
// Cloudflare aliases as distinct provider types. These specs lock the three
// new user-facing surfaces against the seeded baseline (one AWS alias, one
// Cloudflare alias, and the `gamma` env attaching both):
//
//  1. the settings dialog's provider picker + Cloudflare add form,
//  2. the per-provider-type sidebar login rows,
//  3. the per-provider-type env cloud-alias selectors.
//
// The seeded Cloudflare alias has no token in the off-config secret store, so
// its status resolves to not_configured deterministically and offline — the
// scoped-token verify never hits the network. Submitting the add form would
// call the live Cloudflare API, so the add-form spec asserts the form's
// presence, masking, and validation gating without submitting; the Go unit
// coverage for the verify/store round-trip lives in erun-common
// (cloud_cloudflare.go) and the erun-cli integration goldens.

test.describe('multi-provider cloud aliases', () => {
  test('settings provider picker reveals the masked Cloudflare add form', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // Both seeded aliases render, grouped under their provider-type headings.
    await expect(app.globalConfigDialog.cloudAliasGroupHeading('aws')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasGroupHeading('cloudflare')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUDFLARE_ALIAS)).toBeVisible();

    // The provider picker offers both providers; Cloudflare reveals the inline
    // masked-token form (no terminal/PTY).
    await expect(app.globalConfigDialog.addAWSButton()).toBeVisible();
    await app.globalConfigDialog.openCloudflareForm();
    await expect(app.globalConfigDialog.cloudflareForm()).toBeVisible();

    // The API token field is masked (error prevention / shoulder-surfing).
    await expect(app.globalConfigDialog.cloudflareApiTokenInput()).toHaveAttribute(
      'type',
      'password',
    );

    // Submit stays disabled until every field is filled (error prevention,
    // Nielsen #5). Filling the form enables it; we do not submit because the
    // verify would call the live Cloudflare API.
    await expect(app.globalConfigDialog.cloudflareSubmitButton()).toBeDisabled();
    await app.globalConfigDialog.fillCloudflareForm({
      accountId: '0123456789abcdef',
      tokenName: 'pw-new-token',
      apiToken: 'cf-test-token-value',
    });
    await expect(app.globalConfigDialog.cloudflareSubmitButton()).toBeEnabled();

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('sidebar shows one independent login row per provider type', async ({ app }) => {
    // Selecting any pw env makes the tenant's cloud rows the active set. gamma
    // is a seeded baseline env under pw, which references both aliases.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_GAMMA);

    // Two rows render — one per provider type — each labelled by its alias.
    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.sidebar.cloudAliasRowTrigger(SEED_CLOUDFLARE_ALIAS)).toBeVisible();
    await expect.poll(() => app.sidebar.cloudAliasRowCount()).toBe(2);

    // The Cloudflare row's popover offers "Verify token" (a re-verify, not a
    // browser SSO) and hides "Get bearer token" (Cloudflare has no OIDC).
    await app.sidebar.openCloudAliasPopover(SEED_CLOUDFLARE_ALIAS);
    await expect(app.sidebar.cloudAliasPopoverButton(/Verify token/)).toBeVisible();
    await expect(app.sidebar.cloudAliasPopoverButton(/Get bearer token/)).toHaveCount(0);
  });

  test('env manage dialog renders a selector per provider type', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_GAMMA);
    await app.manageDialog.waitForOpen();

    // gamma attaches both aliases, so both per-type selectors render, each
    // pre-selected to the env's attachment for that type.
    expect(await app.manageDialog.cloudAliasSlotVisible('aws')).toBe(true);
    expect(await app.manageDialog.cloudAliasSlotVisible('cloudflare')).toBe(true);
    await expect.poll(() => app.manageDialog.cloudAliasSlotValue('aws')).toBe(SEED_CLOUD_ALIAS);
    await expect
      .poll(() => app.manageDialog.cloudAliasSlotValue('cloudflare'))
      .toBe(SEED_CLOUDFLARE_ALIAS);

    // Each selector independently offers a "— None —" clear option. Clear the
    // Cloudflare slot and confirm only it returns to the placeholder while the
    // AWS slot stays attached (per-type independence). Cancel without saving so
    // the seeded config is untouched.
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

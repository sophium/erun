import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_CLOUDFLARE_ALIAS,
  SEED_ENV_GAMMA,
  SEED_TENANT,
} from '../fixtures/seedRoot.js';

// Multi-provider cloud aliases (issue #630, #632): the desktop surfaces AWS and
// Cloudflare aliases as distinct provider types. These specs lock the three
// user-facing surfaces against the seeded baseline (one AWS alias, one
// Cloudflare alias, and the `gamma` env attaching both):
//
//  1. the settings dialog's provider picker, which delegates BOTH providers'
//     add-alias flows to the CLI's guided `erun cloud init <provider>` PTY,
//  2. the per-provider-type sidebar login rows,
//  3. the per-provider-type env cloud-alias selectors.
//
// Add-alias is delegated to the CLI for every provider type (issue #632): there
// is no in-app add form. Clicking either "AWS" or "Cloudflare" in the picker
// launches the guided CLI flow over a PTY and closes the settings dialog,
// handing the terminal over to the CLI. The harness's inert `erun` stub exits 0
// for `cloud init` without driving the prompts, so the observable invariants the
// spec locks are: the add button closes the dialog (the session took over), no
// in-app add-token form ever renders, and the session-exit toast names the
// provider the operator chose (issue #641 — it previously hardcoded "AWS" for
// every cloud-init session). The guided prompt/verify/resolve flow itself is
// owned and tested by the CLI (erun-common cloud_cloudflare.go + the erun-cli
// integration goldens).

test.describe('multi-provider cloud aliases', () => {
  test('settings provider picker delegates both add flows to the CLI', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // Both seeded aliases render, grouped under their provider-type headings.
    await expect(app.globalConfigDialog.cloudAliasGroupHeading('aws')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasGroupHeading('cloudflare')).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUD_ALIAS)).toBeVisible();
    await expect(app.globalConfigDialog.cloudAliasRow(SEED_CLOUDFLARE_ALIAS)).toBeVisible();

    // The provider picker offers both providers as explicit buttons.
    await expect(app.globalConfigDialog.addAWSButton()).toBeVisible();
    await expect(app.globalConfigDialog.addCloudflareButton()).toBeVisible();

    // Clicking Cloudflare launches the guided CLI flow (a PTY session running
    // `erun cloud init cloudflare`) and closes the settings dialog — exactly
    // like AWS. No in-app "add token" form is ever revealed.
    await app.globalConfigDialog.clickAddCloudflare();
    await app.globalConfigDialog.waitForClosed();
    await expect(app.globalConfigDialog.cloudflareForm()).toHaveCount(0);

    // The harness's inert `erun` stub exits 0 for `cloud init`, so the PTY
    // session ends and the exit toast fires. It must name the provider the
    // session actually set up — "Cloudflare", not the hardcoded "AWS" the exit
    // reason used to emit for every cloud-init session (issue #641).
    await expect(app.titlebar.statusMessage()).toContainText('Cloudflare cloud alias setup ended.');
  });

  test('settings AWS add also delegates to the CLI', async ({ app }) => {
    // The AWS add mirrors Cloudflare: clicking it launches `erun cloud init
    // aws` over a PTY and closes the dialog. Asserting both paths the same way
    // is the consistency invariant issue #632 enforces (Nielsen #4).
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.clickAddAWS();
    await app.globalConfigDialog.waitForClosed();

    // The AWS exit toast still names AWS — the per-provider exit reason (issue
    // #641) keeps the existing label for the AWS path.
    await expect(app.titlebar.statusMessage()).toContainText('AWS cloud alias setup ended.');
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

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/AppShell.js';

// Regression coverage for #359: the Cloud provider `SelectField` wrapper
// inside GlobalConfigDialog.CloudContexts.tsx's `grid gap-2 sm:grid-cols-2`
// is a grid item that defaults to `min-width: auto`, so it refused to shrink
// below its own long alias text and bled across the column boundary into the
// Region trigger next to it -- covering its left side (the report's example:
// "eu-west-2 (London)" read as "u-west-2 (London)"). `SelectField` itself now
// carries `min-w-0` (on its wrapper, its trigger, and the trigger's
// select-value child), and erun-kit's theme.css adds a defensive default for
// any child of a `grid-cols-*` container, so this asserts the actual failure
// mode -- no overlap between the two triggers -- rather than only that the
// dialog renders.
//
// The `sm:` breakpoint is keyed off the *viewport*, not the dialog's own
// (fixed ~512px) width, so 630px/640px straddle the real transition between
// the fields stacking (single column, no overlap possible, covered by the
// first describe block below) and sitting side by side (two columns, where
// the historical bleed happened, covered by the second).
//
// Most widths this spec uses sit below the shell's own sidebar
// auto-collapse breakpoint (758px, `narrow-viewport-shell.spec.ts`), so the
// sidebar starts collapsed there and its "Open ERun settings" trigger is not
// reachable until `Titlebar.toggleSidebar()` opens it back up first.
const SIDEBAR_COLLAPSE_BREAKPOINT = 758;

async function openSettingsAtWidth(app: AppShell, width: number): Promise<void> {
  if (width < SIDEBAR_COLLAPSE_BREAKPOINT) {
    await app.titlebar.toggleSidebar();
  }
  await app.sidebar.openSettings();
}

async function setLongProviderValue(page: import('@playwright/test').Page): Promise<void> {
  const trigger = page.locator('#global-config-cloudcontext-provider');
  await trigger.waitFor({ state: 'visible' });
  await trigger.evaluate((btn) => {
    const span = btn.querySelector('[data-slot="select-value"]');
    if (!(span instanceof HTMLElement)) {
      throw new Error('select-value span not found on Cloud provider trigger');
    }
    span.textContent = 'Rihards.Freimanis+0203626script06330@aws-eu-west-2-production-account';
  });
}

for (const width of [630]) {
  test.describe(`cloud context provider/region fields below the sm breakpoint at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`the fields stack instead of sitting on a shared row at ${width}px`, async ({
      app,
      page,
    }) => {
      await openSettingsAtWidth(app, width);
      await app.globalConfigDialog.waitForOpen();
      await setLongProviderValue(page);

      const provider = app.globalConfigDialog.cloudContextProviderTrigger();
      const region = app.globalConfigDialog.cloudContextRegionTrigger();
      await expect(region).toBeVisible();

      const providerBox = await boundingBoxOf(provider, `Cloud provider trigger at ${width}px`);
      const regionBox = await boundingBoxOf(region, `Region trigger at ${width}px`);

      // Below the breakpoint the section is a single column: the fields
      // stack instead of sitting side by side, so there is no shared row
      // for one to bleed into.
      expect(regionBox.y).toBeGreaterThanOrEqual(providerBox.y + providerBox.height);

      await app.globalConfigDialog.cancel();
      await app.globalConfigDialog.waitForClosed();
    });
  });
}

for (const width of [640, 700, 1440]) {
  test.describe(`cloud context provider/region fields at or above the sm breakpoint at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`a long Cloud provider value does not bleed into the Region field at ${width}px`, async ({
      app,
      page,
    }) => {
      await openSettingsAtWidth(app, width);
      await app.globalConfigDialog.waitForOpen();
      await setLongProviderValue(page);

      const provider = app.globalConfigDialog.cloudContextProviderTrigger();
      const region = app.globalConfigDialog.cloudContextRegionTrigger();
      await expect(region).toBeVisible();

      const providerBox = await boundingBoxOf(provider, `Cloud provider trigger at ${width}px`);
      const regionBox = await boundingBoxOf(region, `Region trigger at ${width}px`);

      // Side by side: the provider trigger's own right edge must not reach
      // past the region trigger's left edge. This is the literal reported
      // symptom -- the provider's rendered content covering the region
      // trigger's left side -- not just "the dialog didn't overflow".
      expect(providerBox.x + providerBox.width).toBeLessThanOrEqual(regionBox.x);

      // And neither trigger may spill past the dialog's own clamped card.
      const card = await boundingBoxOf(
        app.globalConfigDialog.locator(),
        `ERun settings dialog at ${width}px`,
      );
      expect(providerBox.x + providerBox.width).toBeLessThanOrEqual(card.x + card.width + 1);
      expect(regionBox.x + regionBox.width).toBeLessThanOrEqual(card.x + card.width + 1);

      await app.globalConfigDialog.cancel();
      await app.globalConfigDialog.waitForClosed();
    });
  });
}

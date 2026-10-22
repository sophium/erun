import { boundingBoxOf, type ElementBox } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/AppShell.js';

// Regression coverage for the Cloud provider `SelectField` wrapper
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

const LONG_PROVIDER_VALUE = 'Rihards.Freimanis+0203626script06330@aws-eu-west-2-production-account';

async function openSettingsAtWidth(app: AppShell, width: number): Promise<void> {
  if (width < SIDEBAR_COLLAPSE_BREAKPOINT) {
    await app.titlebar.toggleSidebar();
  }
  await app.sidebar.openSettings();
}

async function setLongProviderValue(page: import('@playwright/test').Page): Promise<void> {
  const trigger = page.locator('#global-config-cloudcontext-provider');
  await trigger.waitFor({ state: 'visible' });
  await trigger.evaluate((btn, value) => {
    const span = btn.querySelector('[data-slot="select-value"]');
    if (!(span instanceof HTMLElement)) {
      throw new Error('select-value span not found on Cloud provider trigger');
    }
    span.textContent = value;
  }, LONG_PROVIDER_VALUE);
}

// The one path every width case measures through -- the shared case below and
// each of the four width cases.
//
// The long value this assertion is about is written straight into a
// React-owned span, so it is the render, not the write, that decides what the
// trigger carries: a remount of the draft form puts React's own (short) alias
// back. Geometry read after such a revert describes a trigger with nothing
// long in it, and "no overlap" would be reported while the condition the
// report described -- a long alias bleeding into the Region field -- never
// existed. Asserting the value is still rendered in the same step that takes
// the geometry is what keeps the measurements about the reported condition:
// a reverted trigger fails here rather than being measured as if it were short.
async function measureProviderAgainstRegion(
  app: AppShell,
  label: string,
): Promise<{ providerBox: ElementBox; regionBox: ElementBox }> {
  const provider = app.globalConfigDialog.cloudContextProviderTrigger();
  const region = app.globalConfigDialog.cloudContextRegionTrigger();
  await expect(region).toBeVisible();
  await expect(provider).toContainText(LONG_PROVIDER_VALUE);

  return {
    providerBox: await boundingBoxOf(provider, `${label} Cloud provider trigger`),
    regionBox: await boundingBoxOf(region, `${label} Region trigger`),
  };
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

      const { providerBox, regionBox } = await measureProviderAgainstRegion(app, `at ${width}px`);

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

      const { providerBox, regionBox } = await measureProviderAgainstRegion(app, `at ${width}px`);

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

// A provider value put back by a render -- which is what a remount of the draft
// form does -- leaves the trigger carrying the short seeded alias, and every
// geometry assertion above is then true for a reason the report never
// described: there is no long value to bleed. The measurement refuses that
// state, and this pins the refusal: without it the four width cases above go
// green on a trigger that has already reverted.
test('a reverted Cloud provider value is refused rather than measured as short', async ({
  app,
  page,
}) => {
  await openSettingsAtWidth(app, 1440);
  await app.globalConfigDialog.waitForOpen();
  await app.globalConfigDialog.waitForLoadedFrame();
  await setLongProviderValue(page);

  const trigger = app.globalConfigDialog.cloudContextProviderTrigger();
  await expect(trigger).toContainText(LONG_PROVIDER_VALUE);

  // React's own value back, exactly as a remount of the draft form leaves it.
  await trigger.evaluate((btn) => {
    const span = btn.querySelector('[data-slot="select-value"]');
    if (span instanceof HTMLElement) {
      span.textContent = 'pw-aws';
    }
  });
  await expect(trigger).not.toContainText(LONG_PROVIDER_VALUE);

  await expect(measureProviderAgainstRegion(app, 'at 1440px')).rejects.toThrow(LONG_PROVIDER_VALUE);

  await app.globalConfigDialog.cancel();
  await app.globalConfigDialog.waitForClosed();
});

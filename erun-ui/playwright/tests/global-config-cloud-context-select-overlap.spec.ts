import { boundingBoxOf, type ElementBox } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/AppShell.js';

// A grid item defaults to `min-width: auto`, so a long Cloud provider value
// could bleed into the Region trigger. `sm:` keys off the viewport, so 630px
// stacks and 640px+ sits side by side. Below 758px the sidebar auto-collapses.
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

// Assert the long value is still rendered when measuring: a remount restores
// React's short alias, and "no overlap" would then pass vacuously.
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

      // Single column: no shared row to bleed into.
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

      expect(providerBox.x + providerBox.width).toBeLessThanOrEqual(regionBox.x);

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

// Pins that a reverted (short) value fails the measurement instead of passing.
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

  // Restore React's value, as a remount would.
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

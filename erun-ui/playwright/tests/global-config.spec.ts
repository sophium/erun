import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

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

    // The Cloud provider and Region SelectFields sit in adjacent 1fr
    // tracks of a sm:grid-cols-2 grid. A configured alias long enough
    // to exceed the track (e.g. "Rihards.Freimanis+020362606330@aws")
    // used to bleed past the trigger and visually cover the start of
    // the Region trigger (#359). The seeded baseline keeps its alias
    // short and the layout state under test is purely visual, so mutate
    // the select-value span directly to force the same layout state and
    // assert the rendered content stays within its column.
    await provider.evaluate((btn) => {
      const span = btn.querySelector('[data-slot="select-value"]');
      if (!(span instanceof HTMLElement)) {
        throw new Error('select-value span not found on Cloud provider trigger');
      }
      span.textContent = `long.user.name+0123456789@aws-${'x'.repeat(80)}`;
    });

    const providerBox = await provider.boundingBox();
    const valueBox = await app.globalConfigDialog.cloudContextProviderValue().boundingBox();
    const regionBox = await region.boundingBox();
    expect(providerBox).not.toBeNull();
    expect(valueBox).not.toBeNull();
    expect(regionBox).not.toBeNull();
    if (!providerBox || !valueBox || !regionBox) return;
    // The visible content span must not extend past the Cloud provider
    // trigger, and the trigger must not extend past the start of the
    // Region trigger.
    expect(valueBox.x + valueBox.width).toBeLessThanOrEqual(providerBox.x + providerBox.width);
    expect(providerBox.x + providerBox.width).toBeLessThanOrEqual(regionBox.x);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});

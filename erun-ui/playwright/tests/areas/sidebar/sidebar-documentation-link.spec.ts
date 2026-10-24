import { test, expect } from '../../../fixtures/erunApp.js';

// This locks the sidebar's in-app route to documentation. It goes through the Wails runtime
// binding (see documentationThunks.ts) rather than a plain window.open, and
// the headless harness's shim maps that binding to a real window.open — the
// desktop's native "open in a real external browser" behavior itself is not
// observable in this harness and is exercised by hand.
test.describe('in-app documentation link (#1217)', () => {
  test('opens the public docs site', async ({ app, page }) => {
    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      app.sidebar.documentationButton().click(),
    ]);
    // Read the target URL only — this sandbox has no network access, so the
    // popup's navigation itself never has to complete for this assertion.
    await expect.poll(() => popup.url()).toBe('https://docs.erunpaas.com/');
    await popup.close();
  });
});

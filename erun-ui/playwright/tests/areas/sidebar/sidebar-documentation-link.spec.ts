import { test, expect } from '../../../fixtures/erunApp.js';

// The link goes through the Wails runtime binding, which the harness shims to window.open.
test.describe('in-app documentation link (#1217)', () => {
  test('opens the public docs site', async ({ app, page }) => {
    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      app.sidebar.documentationButton().click(),
    ]);
    // The sandbox has no network, so only the popup's URL is read.
    await expect.poll(() => popup.url()).toBe('https://docs.erunpaas.com/');
    await popup.close();
  });
});

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Detection of leftover remote sessions needs a pod and is Go-tested; with
// nothing to detect, opening an env must leave a clean, duplicate-free tab strip.

test.describe('remote session tab detection', () => {
  test('opening an env yields only known tab kinds with no duplicates', async ({ app, page }) => {
    // Detection is fire-and-forget; wait for its RPC to complete.
    const detected = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('ListRemoteAppSessions'),
    );
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const strip = page.getByRole('tablist', { name: 'Open terminals' });
    await expect(strip).toBeVisible();
    await detected;

    const labels = await strip.getByRole('tab').allInnerTexts();
    expect(labels.length).toBeGreaterThan(0);
    const known = /^(Local|ERun|AI|ERun \(contribute\)|AI \(contribute\)|Terminal \d+)/;
    for (const label of labels) {
      expect(label.trim()).toMatch(known);
    }
    const trimmed = labels.map((label) => label.trim());
    expect(new Set(trimmed).size).toBe(trimmed.length);
  });
});

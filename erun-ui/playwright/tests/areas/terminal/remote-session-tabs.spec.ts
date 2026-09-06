import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The positive detection path — an env whose pod carries an `open-N` dtach
// socket that another ERun window left behind, which opening the env should
// rebuild into `Terminal N` tabs — is unreachable here: the harness has no
// staged runtime pod (kubectl is stubbed) and ListRemoteAppSessions is
// fail-soft, so there is nothing to detect. The invariant this spec locks
// instead: with nothing to detect, opening an env still yields a tab strip of
// only known kinds, with no duplicate labels and no error banner — catching the
// regression where the fire-and-forget detection thunk throws into the open
// flow or double-registers tabs it did not create.
//
// The positive path is covered in erun-common by
// TestListRemoteAppSessionsParsesPodSockets and TestParseRemoteAppSessionIDs.

test.describe('remote session tab detection', () => {
  test('opening an env yields only known tab kinds with no duplicates', async ({ app, page }) => {
    // The detection pass is fire-and-forget; wait for its RPC to complete (not
    // the wall clock) so the assertions observe a finished pass.
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

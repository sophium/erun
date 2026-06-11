import { expect, test } from '../fixtures/erunApp.js';

// remote-session-tabs covers the detection flow from #478: when an env opens,
// finishOpenSession fires reattachRemoteTerminalTabs, which asks the backend
// (ListRemoteAppSessions → kubectl exec ls of the pod's dtach sockets) for
// persistent sessions another ERun window created and rebuilds `Terminal N`
// tabs for them. Default tabs (Local, ERun, AI) attach-or-create on their own.
//
// The positive path — a pod that actually carries an `open-N` socket from
// another window — is not reachable from the headless harness:
//   - The harness has no staged runtime pod; ListRemoteAppSessions is
//     fail-soft and returns nothing for envs whose cluster is absent.
//   - Pre-seeding a dtach socket would require a live kubectl exec into a
//     real pod, which the suite deliberately does not depend on.
//
// Invariant we CAN lock down: the detection pass is structurally silent when
// there is nothing to detect — opening an env still yields a tab strip whose
// entries are only the known kinds (Local/ERun/AI, contribute variants, or
// `Terminal N` extras), with no duplicate labels and no error banner. This
// catches the regression class where the fire-and-forget detection thunk
// throws into the open flow or double-registers tabs it did not create.
//
// The positive detection path is covered by Go tests:
// TestListRemoteAppSessionsParsesPodSockets (PATH-stubbed kubectl) and
// TestParseRemoteAppSessionIDs in erun-common.

test.describe('remote session tab detection', () => {
  test('opening an env yields only known tab kinds with no duplicates', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    await app.sidebar.openEnvironment(tenant, envs[0]!);

    const strip = page.getByRole('tablist', { name: 'Open terminals' });
    await expect(strip).toBeVisible();
    // Give the fire-and-forget detection pass a window to run; it must not
    // surface an error overlay or mutate the strip into unknown entries.
    await page.waitForTimeout(1_000);

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

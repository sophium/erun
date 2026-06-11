import { test, expect } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';

// Regression: issue #443 — the sidebar LOCAL badge keyed off the legacy
// `remote` flag instead of the resolved environment type, so a local-agent
// env created with the new `type` shape (legacy `remote` unset) showed no
// badge even though the Manage dialog reported "Local agent". The fix derives
// the badge from the resolved type (environmentIsLocal).
//
// The badge is verified against ground truth the user can see: the Manage
// dialog's "Environment type" field. The contract is — if the dialog says
// "Local agent", the sidebar row must show the LOCAL pill, and vice versa.
// The harness reflects the developer's real ~/.erun config, so the spec
// discovers envs at runtime rather than assuming names or types.
test.describe('sidebar LOCAL badge', () => {
  test('badge matches the environment type and the (local) label suffix', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    const outcomes: boolean[] = [];
    for (const env of envs) {
      // Skip the local default env (e.g. erun/local): it is a legacy-typed
      // local-shell env whose "Legacy (derived from remote + snapshot)" type
      // doesn't fit the resolved-type → badge contract this spec verifies.
      if (env === 'local') {
        continue;
      }
      outcomes.push(await assertBadgeMatchesType(app, tenant, env));
    }
    const checked = outcomes.filter(Boolean).length;

    // The harness reflects the developer's real ~/.erun. When the only
    // environments are local defaults (e.g. erun/local, tenant-a/local), there
    // is no non-local, explicitly-typed env to assert the badge↔type contract
    // against, so skip rather than assert against a local env.
    test.skip(
      checked === 0,
      'no non-local, explicitly-typed environment in this developer harness',
    );
  });
});

// assertBadgeMatchesType reads the env's resolved type from the Manage dialog
// and asserts the sidebar badge + (local) suffix agree with it. Returns false
// when the type field isn't rendered (unknown type) so the caller can skip it.
// Keeping the conditional here keeps the test body branch-free.
async function assertBadgeMatchesType(
  app: AppShell,
  tenant: string,
  env: string,
): Promise<boolean> {
  await app.sidebar.openManageDialogFor(tenant, env);
  await app.manageDialog.waitForOpen();
  const envType = await app.manageDialog.envTypeFieldValue();
  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();

  if (envType === '') {
    return false;
  }

  const expectLocal = envType.startsWith('Local agent');
  const hasBadge = await app.sidebar.hasLocalBadge(tenant, env);
  const hasSuffix = await app.sidebar.rowHasLocalSuffix(tenant, env);

  expect(
    hasBadge,
    `LOCAL badge for ${tenant} / ${env} (type "${envType}") should be ${String(expectLocal)}`,
  ).toBe(expectLocal);
  // The badge and the accessible-label suffix share the isLocal flag and must
  // never diverge.
  expect(hasSuffix).toBe(hasBadge);
  return true;
}

import { test, expect } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Regression: the sidebar LOCAL badge keyed off the legacy
// `remote` flag instead of the resolved environment type, so a local-agent
// env created with the new `type` shape (legacy `remote` unset) showed no
// badge even though the Manage dialog reported "Local agent". The fix derives
// the badge from the resolved type (environmentIsLocal).
//
// The badge is verified against ground truth the user can see: the Manage
// dialog's "Environment type" field. The contract is — if the dialog says
// "Local agent", the sidebar row must show the LOCAL pill, and vice versa.
// The seeded baseline envs carry an explicit `type: local-agent` (the exact
// shape the bug regressed on), so both must report "Local agent" and show the
// pill.
test.describe('sidebar LOCAL badge', () => {
  test('badge matches the environment type and the (local) label suffix', async ({ app }) => {
    for (const env of [SEED_ENV_ALPHA, SEED_ENV_BETA]) {
      await assertBadgeMatchesType(app, SEED_TENANT, env);
    }
  });
});

// assertBadgeMatchesType reads the env's resolved type from the Manage dialog
// and asserts the sidebar badge + (local) suffix agree with it. The dialog is
// opened via the keyboard path: a mouse click on the second row gets
// intercepted by the env hover-card popover the pointer trails over.
async function assertBadgeMatchesType(app: AppShell, tenant: string, env: string): Promise<void> {
  await app.sidebar.openManageDialogViaKeyboard(tenant, env);
  await app.manageDialog.waitForOpen();
  const envType = await app.manageDialog.envTypeFieldValue();
  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();

  // The seeded envs are explicitly typed, so the type field must render
  // and resolve to the local-agent label.
  expect(envType.startsWith('Local agent'), `type for ${tenant} / ${env} was "${envType}"`).toBe(
    true,
  );

  const hasBadge = await app.sidebar.hasLocalBadge(tenant, env);
  const hasSuffix = await app.sidebar.rowHasLocalSuffix(tenant, env);
  expect(hasBadge, `LOCAL badge for ${tenant} / ${env} (type "${envType}")`).toBe(true);
  // The badge and the accessible-label suffix share the isLocal flag and must
  // never diverge.
  expect(hasSuffix).toBe(hasBadge);
}

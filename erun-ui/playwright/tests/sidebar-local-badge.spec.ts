import { test, expect } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Regression guard: the sidebar LOCAL badge once keyed off the legacy `remote`
// flag instead of the resolved env type, so a local-agent env in the new
// `type` shape (`remote` unset) showed no badge even though the Manage dialog
// reported "Local agent". The seeded envs carry that exact shape, so the
// sidebar pill must agree with the dialog's reported type.
test.describe('sidebar LOCAL badge', () => {
  test('badge matches the environment type and the (local) label suffix', async ({ app }) => {
    for (const env of [SEED_ENV_ALPHA, SEED_ENV_BETA]) {
      await assertBadgeMatchesType(app, SEED_TENANT, env);
    }
  });
});

// The dialog is opened via the keyboard path because a mouse click on the row
// gets intercepted by the env hover-card popover the pointer trails over.
async function assertBadgeMatchesType(app: AppShell, tenant: string, env: string): Promise<void> {
  await app.sidebar.openManageDialogViaKeyboard(tenant, env);
  await app.manageDialog.waitForOpen();
  const envType = await app.manageDialog.envTypeFieldValue();
  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();

  expect(envType.startsWith('Local agent'), `type for ${tenant} / ${env} was "${envType}"`).toBe(
    true,
  );

  const hasBadge = await app.sidebar.hasLocalBadge(tenant, env);
  const hasSuffix = await app.sidebar.rowHasLocalSuffix(tenant, env);
  expect(hasBadge, `LOCAL badge for ${tenant} / ${env} (type "${envType}")`).toBe(true);
  expect(hasSuffix).toBe(hasBadge);
}

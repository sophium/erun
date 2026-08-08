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
      const observed = await readBadgeState(app, SEED_TENANT, env);
      expect(observed.envType, `Manage dialog type for ${SEED_TENANT} / ${env}`).toMatch(
        /^Local agent/,
      );
      expect(
        observed.hasBadge,
        `LOCAL badge for ${SEED_TENANT} / ${env} (type "${observed.envType}")`,
      ).toBe(true);
      expect(observed.hasSuffix, `"(local)" row-label suffix for ${SEED_TENANT} / ${env}`).toBe(
        true,
      );
    }
  });
});

interface BadgeState {
  envType: string;
  hasBadge: boolean;
  hasSuffix: boolean;
}

// The dialog is opened via the keyboard path because a mouse click on the row
// gets intercepted by the env hover-card popover the pointer trails over.
async function readBadgeState(app: AppShell, tenant: string, env: string): Promise<BadgeState> {
  await app.sidebar.openManageDialogViaKeyboard(tenant, env);
  await app.manageDialog.waitForOpen();
  const envType = await app.manageDialog.envTypeFieldValue();
  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();

  return {
    envType,
    hasBadge: await app.sidebar.hasLocalBadge(tenant, env),
    hasSuffix: await app.sidebar.rowHasLocalSuffix(tenant, env),
  };
}

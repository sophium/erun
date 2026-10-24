import { test, expect } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The LOCAL badge must follow the resolved env type, not the legacy `remote` flag, and agree with
// the Manage dialog.
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

// A mouse click on the row is intercepted by the hover-card popover, so open via keyboard.
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

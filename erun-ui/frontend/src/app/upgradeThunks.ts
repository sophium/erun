import type { StartSessionResult, UISelection, UIUpgradePlanItem } from '@/types';

import { ResolveUpgradePlan, StartUpgradeEnvironmentSession } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showNotification, showTerminalMessage } from './notificationThunks';
import {
  closeUpgradeAllDialog,
  openUpgradeAllDialog,
  setUpgradeAllError,
  setUpgradeAllPlan,
} from './slices/upgradeAllSlice';
import type { AppThunk } from './store';
import { recordTab } from './tabsThunks';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// openUpgradeAll opens the Upgrade-all preview dialog and resolves the plan
// (every opted-in env, its channel, current → target). Read-only — nothing
// deploys until the user confirms. The plan resolver is the same one the
// per-env runs use (issue #497), so the preview never promises an upgrade
// the run would refuse.
export const openUpgradeAll = (): AppThunk<Promise<void>> => async (dispatch) => {
  dispatch(openUpgradeAllDialog());
  try {
    const plan = await ResolveUpgradePlan();
    dispatch(setUpgradeAllPlan(plan.items as UIUpgradePlanItem[]));
  } catch (error) {
    dispatch(setUpgradeAllError(readError(error)));
  }
};

// confirmUpgradeAll fans the upgrade out per member (issue #497): every
// lagging plan member runs `erun upgrade --tenant <t> --environment <e>` in
// its OWN Local shell, so members upgrade in parallel and output, activity
// entries, and failures land on the env they belong to — not on one host
// env's terminal. The fan-out deliberately does not change the selection or
// ensure any env's default tabs: selecting an env subjects it to the
// selected-env machinery (which keeps its tab set — including the AI tab —
// alive), and confirming an upgrade is not an open intent; a Claude session
// must never launch as a side effect of clicking Upgrade. Progress is
// visible through each member's Local tab, sidebar row, and activity entry,
// announced by the confirmation toast (Nielsen #1).
export const confirmUpgradeAll =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const state = getState();
    const lagging = state.upgradeAll.items.filter((item) => item.lagging);
    dispatch(closeUpgradeAllDialog());
    if (lagging.length === 0) {
      dispatch(
        showTerminalMessage('Nothing to upgrade — no opted-in environment lags its channel.'),
      );
      return;
    }
    const controller = requireController(extra);
    const debugOpen = state.layout.debugOpen;
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    const outcomes = await Promise.allSettled(
      lagging.map(async (member) => {
        const selection: UISelection = {
          tenant: member.tenant,
          environment: member.environment,
          debug: debugOpen || undefined,
        };
        const result = (await StartUpgradeEnvironmentSession(
          selection,
          cols,
          rows,
        )) as StartSessionResult;
        // Register the Local session the run lives in so its tab renders,
        // WITHOUT ensuring the env's default tab set.
        dispatch(
          recordTab(selectionKey(selection), result.sessionId, result.slot ?? 0, 'local', 'Local'),
        );
        return member;
      }),
    );

    const failed: string[] = [];
    outcomes.forEach((outcome, index) => {
      const member = lagging[index];
      if (outcome.status === 'rejected' && member) {
        failed.push(`${member.tenant}/${member.environment}`);
      }
    });
    if (failed.length > 0) {
      dispatch(
        showNotification(
          'error',
          `Could not start the upgrade for ${failed.join(', ')} — see the Local tab and Activities.`,
        ),
      );
      return;
    }
    const only = lagging.length === 1 ? lagging[0] : undefined;
    dispatch(
      showNotification(
        'info',
        only
          ? `Upgrading ${only.tenant} / ${only.environment} — follow progress in its Local tab and Activities.`
          : `Upgrading ${String(lagging.length)} environments in parallel — follow progress in their Local tabs and Activities.`,
      ),
    );
  };

export { closeUpgradeAllDialog };

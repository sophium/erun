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

// openUpgradeAll shows the read-only Upgrade-all preview: nothing deploys until
// the user confirms, and it resolves the plan with the same resolver the per-env
// runs use, so the preview never promises an upgrade the run would refuse.
export const openUpgradeAll = (): AppThunk<Promise<void>> => async (dispatch) => {
  dispatch(openUpgradeAllDialog());
  try {
    const plan = await ResolveUpgradePlan();
    dispatch(setUpgradeAllPlan(plan.items as UIUpgradePlanItem[]));
  } catch (error) {
    dispatch(setUpgradeAllError(readError(error)));
  }
};

// confirmUpgradeAll runs each member's upgrade in its OWN Local shell so members
// upgrade in parallel and each env's output, activity, and failures stay on the
// env they belong to. It deliberately does not select the env or ensure its
// default tabs: confirming an upgrade is not an open intent, so a Claude session
// must never launch as a side effect of clicking Upgrade.
export const confirmUpgradeAll =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const state = getState();
    const members = upgradeMembers(state.upgradeAll.items, state.upgradeAll.choices);
    dispatch(closeUpgradeAllDialog());
    if (members.length === 0) {
      dispatch(
        showTerminalMessage(
          'Nothing to upgrade — no opted-in environment lags its channel or has a version picked.',
        ),
      );
      return;
    }
    const controller = requireController(extra);
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();

    const outcomes = await Promise.allSettled(
      members.map(async (member) => {
        const selection: UISelection = {
          tenant: member.tenant,
          environment: member.environment,
          version: member.version,
        };
        const result = (await StartUpgradeEnvironmentSession(
          selection,
          cols,
          rows,
        )) as StartSessionResult;
        dispatch(
          recordTab(selectionKey(selection), result.sessionId, result.slot ?? 0, 'local', 'Local'),
        );
        return member;
      }),
    );

    const failed: string[] = [];
    outcomes.forEach((outcome, index) => {
      const member = members[index];
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
    const only = members.length === 1 ? members[0] : undefined;
    dispatch(
      showNotification(
        'info',
        only
          ? `Upgrading ${only.tenant} / ${only.environment} — follow progress in its Local tab and Activities.`
          : `Upgrading ${String(members.length)} environments in parallel — follow progress in their Local tabs and Activities.`,
      ),
    );
  };

// UpgradeMember is one environment the confirmed Upgrade-all run will redeploy:
// a lagging env (no version — the CLI resolves its single channel target) or an
// ambiguous env the operator picked a version for.
interface UpgradeMember {
  tenant: string;
  environment: string;
  version?: string;
}

// upgradeMembers must mirror the dialog's enable/count logic so what runs
// matches what the Upgrade button promised.
function upgradeMembers(
  items: UIUpgradePlanItem[],
  choices: Record<string, string>,
): UpgradeMember[] {
  const members: UpgradeMember[] = [];
  for (const item of items) {
    if (item.lagging) {
      members.push({ tenant: item.tenant, environment: item.environment });
      continue;
    }
    if ((item.candidates?.length ?? 0) > 1) {
      const version = (
        choices[selectionKey({ tenant: item.tenant, environment: item.environment })] ?? ''
      ).trim();
      if (version !== '') {
        members.push({ tenant: item.tenant, environment: item.environment, version });
      }
    }
  }
  return members;
}

export { closeUpgradeAllDialog };

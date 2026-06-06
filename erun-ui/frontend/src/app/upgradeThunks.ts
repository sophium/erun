import type { StartSessionResult, UISelection, UIUpgradePlanItem } from '@/types';

import { ResolveUpgradePlan, StartUpgradeAllSession } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { activateLocalAfterCommand } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import {
  closeUpgradeAllDialog,
  openUpgradeAllDialog,
  setUpgradeAllError,
  setUpgradeAllPlan,
} from './slices/upgradeAllSlice';
import type { AppThunk, RootState } from './store';
import { requireController } from './thunkExtra';

// openUpgradeAll opens the Upgrade-all preview dialog and resolves the plan
// (every opted-in env, its channel, current → target). Read-only — nothing
// deploys until the user confirms.
export const openUpgradeAll = (): AppThunk<Promise<void>> => async (dispatch) => {
  dispatch(openUpgradeAllDialog());
  try {
    const plan = await ResolveUpgradePlan();
    dispatch(setUpgradeAllPlan(plan.items as UIUpgradePlanItem[]));
  } catch (error) {
    dispatch(setUpgradeAllError(readError(error)));
  }
};

// resolveUpgradeHostSelection picks the environment whose Local shell hosts the
// `erun upgrade` run. `erun upgrade` is global — it redeploys every opted-in
// env itself — so the host only supplies a shell to run the command in. Run it
// from an environment that is actually being upgraded so the command and its
// output land in that env's terminal rather than an unrelated one (e.g. the
// local default): prefer a lagging plan member, then any plan member, then the
// open env, then any configured env. Resolving from the plan means Upgrade all
// runs without the operator opening an environment first.
function resolveUpgradeHostSelection(state: RootState): UISelection | null {
  const items = state.upgradeAll.items;
  const member = items.find((item) => item.lagging) ?? items[0];
  if (member) {
    return { tenant: member.tenant, environment: member.environment };
  }
  const selected = state.selection.selected;
  if (selected) {
    return selected;
  }
  for (const tenant of state.tenants.tenants) {
    const host =
      tenant.environments.find((env) => env.name === tenant.defaultEnvironment) ??
      tenant.environments[0];
    if (host) {
      return { tenant: tenant.name, environment: host.name };
    }
  }
  return null;
}

// confirmUpgradeAll runs the global `erun upgrade` in the Local shell of an
// environment that is being upgraded (the command itself redeploys every
// lagging opted-in env; the host env supplies the shell and the terminal the
// run is shown in). Each composed deploy surfaces an activity-queue entry, like
// a normal deploy. It resolves the host from the plan, so the operator does not
// have to open an environment first and the run lands in a relevant env.
export const confirmUpgradeAll =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const selection = resolveUpgradeHostSelection(getState());
    if (!selection) {
      dispatch(closeUpgradeAllDialog());
      dispatch(showTerminalMessage('No environments are configured to upgrade yet.'));
      return;
    }
    const controller = requireController(extra);
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    dispatch(closeUpgradeAllDialog());
    dispatch(setSelected(selection));
    dispatch(showTerminalMessage('Upgrading all opted-in environments...', true));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await StartUpgradeAllSession(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

export { closeUpgradeAllDialog };

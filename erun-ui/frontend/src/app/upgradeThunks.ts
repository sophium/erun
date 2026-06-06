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
import type { AppThunk } from './store';
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

// confirmUpgradeAll runs the global `erun upgrade` in the currently selected
// env's Local shell (the command itself redeploys every lagging opted-in env;
// the selection only supplies the shell). Each composed deploy surfaces an
// activity-queue entry, like a normal deploy. Requires an open/selected env.
export const confirmUpgradeAll =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const selection: UISelection | null = getState().selection.selected;
    if (!selection) {
      dispatch(closeUpgradeAllDialog());
      dispatch(showTerminalMessage('Open an environment first to run Upgrade all.'));
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

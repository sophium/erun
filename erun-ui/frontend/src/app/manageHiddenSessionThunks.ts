import type { StartSessionResult, UISelection } from '@/types';

import { StartDoctorSession, StartSSHDInitSession } from '../../wailsjs/go/main/App';
import type { HiddenSessionMode } from './model';
import { showTerminalMessage } from './notificationThunks';
import { activateLocalAfterCommand } from './sessionThunks';
import { setManageDialog } from './slices/manageDialogSlice';
import { setSelected } from './slices/selectionSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import { defaultManageDialog } from './state';
import type { AppThunk } from './store';
import { hiddenSessionBusyMessage } from './terminalStatus';
import { requireController } from './thunkExtra';

export const enableManageSSHD = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (dialog.busy || dialog.configLoading || !selection) {
    return;
  }
  await dispatch(startHiddenSession('sshd-init', selection, StartSSHDInitSession));
};

// startManageDoctor targets the selected environment: it is reachable with no
// Manage dialog open.
export const startManageDoctor = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const dialog = state.manageDialog;
  const selection = dialog.selection ?? state.selection.selected;
  if (!selection) {
    dispatch(showTerminalMessage('Select an environment first to run doctor.'));
    return;
  }
  if (dialog.open && (dialog.busy || dialog.configLoading)) {
    return;
  }
  await dispatch(startHiddenSession('doctor', selection, StartDoctorSession));
};

// Hidden sessions always run in the shared Local shell and report completion via
// trace-driven events, never a PTY exit, so other kinds are refused.
const startHiddenSession =
  (
    mode: HiddenSessionMode,
    selection: UISelection,
    starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
  ): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const runSelection = { ...selection };
    dispatch(setSelected(selection));
    dispatch(setManageDialog(defaultManageDialog()));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(hiddenSessionBusyMessage(selection, mode), true));
    controller.fitTerminal();
    const size = controller.terminalSize();
    const result = (await starter(runSelection, size.cols, size.rows)) as StartSessionResult;
    if (result.kind !== 'local') {
      throw new Error(`hidden session '${mode}' returned unexpected kind '${result.kind ?? ''}'`);
    }
    await dispatch(activateLocalAfterCommand(selection, result));
  };

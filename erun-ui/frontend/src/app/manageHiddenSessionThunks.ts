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

// Unlike enableManageSSHD, doctor is also reachable from the sidebar's
// stethoscope button with no Manage dialog open — so it targets the
// currently selected environment rather than the dialog's own selection,
// which is only ever set together with the dialog itself opening.
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

// Both HiddenSessionMode starters (StartSSHDInitSession, StartDoctorSession)
// pipe their command into the shared Local shell and always come back as a
// 'local' session, never a dedicated PTY with its own exit event (see
// erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring"). Each
// command reports its own completion through a trace-line-driven Wails event
// instead (doctor-completed, sshd-init-completed) — that tracking was
// unreachable dead code for both commands until each was wired up in turn
// (erun#1268, erun#1276), because this thunk kept a second, PTY-exit-shaped
// tracking path alive for a kind that never arrives. Refusing a kind other
// than 'local' here closes that seam: a future starter that returns a
// different kind cannot silently repeat the same mistake a third time.
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

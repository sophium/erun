import type { StartSessionResult, UISelection } from '@/types';

import { StartDoctorSession, StartSSHDInitSession } from '../../wailsjs/go/main/App';
import { applyPendingDebugHeader, setPendingDebugHeader, syncDebugDisplay } from './debugThunks';
import type { HiddenSessionMode } from './model';
import { showTerminalMessage } from './notificationThunks';
import { activateLocalAfterCommand } from './sessionThunks';
import { setManageDialog } from './slices/manageDialogSlice';
import { setSelected } from './slices/selectionSlice';
import {
  registerDebugSession,
  trackDoctorSession,
  trackSSHDInitSession,
} from './slices/sessionsSlice';
import { setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import { defaultManageDialog } from './state';
import type { AppThunk } from './store';
import { formatDebugCommand, hiddenSessionBusyMessage } from './terminalStatus';
import { requireController } from './thunkExtra';

export const enableManageSSHD = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(startHiddenSession('sshd-init', StartSSHDInitSession));
};

export const startManageDoctor = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(startHiddenSession('doctor', StartDoctorSession));
};

const startHiddenSession =
  (
    mode: HiddenSessionMode,
    starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const dialog = state.manageDialog;
    const selection = dialog.selection;
    if (dialog.busy || dialog.configLoading || !selection) {
      return;
    }
    const debugOpen = state.layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    dispatch(setSelected(selection));
    dispatch(setManageDialog(defaultManageDialog()));
    if (debugOpen) {
      dispatch(setPendingDebugHeader(`$ ${formatDebugCommand(runSelection, mode)}\n`));
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(hiddenSessionBusyMessage(selection, mode), true));
    controller.fitTerminal();
    const size = controller.terminalSize();
    const result = (await starter(runSelection, size.cols, size.rows)) as StartSessionResult;
    if (result.kind === 'local') {
      await dispatch(activateLocalAfterCommand(selection, result));
      return;
    }
    dispatch(trackHiddenSession(mode, result.sessionId, runSelection));
    dispatch(
      registerDebugSession({
        sessionId: result.sessionId,
        selection: runSelection,
        mode: 'hidden',
      }),
    );
    dispatch(applyPendingDebugHeader(result.sessionId));
    dispatch(setSessionId(result.sessionId));
    dispatch(syncDebugDisplay());
    controller.resetTerminal();
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  };

const trackHiddenSession =
  (mode: HiddenSessionMode, sessionId: number, selection: UISelection): AppThunk =>
  (dispatch) => {
    if (mode === 'sshd-init') {
      dispatch(trackSSHDInitSession({ sessionId, selection }));
      return;
    }
    dispatch(trackDoctorSession({ sessionId, selection }));
  };

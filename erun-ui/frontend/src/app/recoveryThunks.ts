import type { StartSessionResult, UISelection } from '@/types';

import { StartDoctorSession, StartForceDeploySession } from '../../wailsjs/go/main/App';
import { showTerminalMessage } from './notificationThunks';
import { activateLocalAfterCommand } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

// Run in the visible Local tab; hidden, users re-click and flood it.
const runLocalRecoverySelection =
  (
    selection: UISelection,
    busyMessage: string,
    starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
  ): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const runSelection = { ...selection };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(busyMessage, true));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await starter(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

export const startDoctorSelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runLocalRecoverySelection(
    selection,
    `Running doctor for ${selection.tenant} / ${selection.environment}...`,
    StartDoctorSession,
  );

export const startForceDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runLocalRecoverySelection(
    selection,
    `Rebuilding & redeploying ${selection.tenant} / ${selection.environment}...`,
    StartForceDeploySession,
  );

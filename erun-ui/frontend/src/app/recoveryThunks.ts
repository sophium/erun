import type { StartSessionResult, UISelection } from '@/types';

import { StartDoctorSession, StartForceDeploySession } from '../../wailsjs/go/main/App';
import { showTerminalMessage } from './notificationThunks';
import { activateLocalAfterCommand } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

// runLocalRecoverySelection runs an `erun …` recovery command for the given env
// in the shared Local shell and focuses the Local tab so the user sees it.
// Without activateLocalAfterCommand the command runs in a background shell with
// no visible feedback, which led users to click repeatedly and flood the shell
// (#445). Mirrors startDeploySelection in sessionThunks.
const runLocalRecoverySelection =
  (
    selection: UISelection,
    busyMessage: string,
    starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const debugOpen = getState().layout.debugOpen;
    const runSelection = { ...selection, debug: debugOpen || undefined };
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(busyMessage, true));
    controller.fitTerminal();
    const { cols, rows } = controller.terminalSize();
    const result = (await starter(runSelection, cols, rows)) as StartSessionResult;
    await dispatch(activateLocalAfterCommand(selection, result));
  };

// startDoctorSelection runs `erun doctor` for the env behind a failed deploy
// card's "Run doctor" button.
export const startDoctorSelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runLocalRecoverySelection(
    selection,
    `Running doctor for ${selection.tenant} / ${selection.environment}...`,
    StartDoctorSession,
  );

// startForceDeploySelection runs `erun deploy --force` for the env behind a
// failed deploy card's (or a failing container's) "Rebuild & redeploy" button.
export const startForceDeploySelection = (selection: UISelection): AppThunk<Promise<void>> =>
  runLocalRecoverySelection(
    selection,
    `Rebuilding & redeploying ${selection.tenant} / ${selection.environment}...`,
    StartForceDeploySession,
  );

import type { UISelection } from '@/types';

import { sessionApi } from './api/sessionApi';
import { appendDebugOutput, syncDebugDisplay } from './debugThunks';
import { readError } from './errors';
import type { IDEKind } from './model';
import {
  dismissNotification,
  dismissTerminalStatus,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { setSelected } from './slices/selectionSlice';
import { setSessionDebug } from './slices/sessionsSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { debugOutputBlock, formatIDECommand, ideLabel, ideOpenFailure } from './terminalStatus';

export const openIDE =
  (selection: UISelection | null, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (!selection) {
      dispatch(showTerminalMessage('Choose an environment from the left pane.'));
      return;
    }
    const state = getState();
    const runSelection = { ...selection, debug: state.layout.debugOpen || undefined };
    const label = ideLabel(ide);
    dispatch(setSelected(selection));
    if (state.layout.debugOpen) {
      const header = `$ ${formatIDECommand(runSelection, ide)}\n`;
      dispatch(setSessionDebug({ sessionId: getState().terminal.sessionId, value: header }));
      dispatch(syncDebugDisplay());
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`),
    );

    try {
      await dispatch(
        sessionApi.endpoints.openIDE.initiate({ selection: runSelection, ide }),
      ).unwrap();
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
      dispatch(appendDebugOutput(debugOutputBlock(failure.copyOutput)));
      dispatch(dismissNotification());
      dispatch(showTerminalFailure(failure.message, failure.detail, failure.copyOutput, '', null));
      return;
    }
    dispatch(dismissTerminalStatus());
    dispatch(
      showNotification(
        'success',
        `Opened ${label} for ${selection.tenant} / ${selection.environment}.`,
      ),
    );
  };

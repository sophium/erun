import type { UISelection } from '@/types';

import { sessionApi } from './api/sessionApi';
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
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { ideLabel, ideOpenFailure } from './terminalStatus';

export const openIDE =
  (selection: UISelection | null, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch) => {
    if (!selection) {
      dispatch(showTerminalMessage('Choose an environment from the left pane.'));
      return;
    }
    const label = ideLabel(ide);
    dispatch(setSelected(selection));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(`Opening ${label} for ${selection.tenant} / ${selection.environment}...`),
    );

    try {
      await dispatch(sessionApi.endpoints.openIDE.initiate({ selection, ide })).unwrap();
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
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

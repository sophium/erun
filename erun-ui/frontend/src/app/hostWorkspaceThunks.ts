import type { UISelection } from '@/types';

import { OpenHostIDE } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import type { IDEKind } from './model';
import {
  dismissTerminalStatus,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { setSelected } from './slices/selectionSlice';
import type { AppThunk } from './store';
import { ideLabel, ideOpenFailure } from './terminalStatus';

// openHostIDE launches VS Code / IntelliJ against the env's host workspace — the
// local-agent worktree or the remote-agent's synced mirror — rather than the
// in-pod copy. This is what makes IDE review work on Windows, where the in-pod
// worktree is not directly openable by a host editor.
export const openHostIDE =
  (selection: UISelection, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch) => {
    const label = ideLabel(ide);
    dispatch(setSelected(selection));
    dispatch(
      showTerminalMessage(
        `Opening ${label} on the host worktree for ${selection.tenant} / ${selection.environment}...`,
      ),
    );
    try {
      await OpenHostIDE(selection, ide);
    } catch (error: unknown) {
      const failure = ideOpenFailure(selection, label, readError(error));
      dispatch(showTerminalFailure(failure.message, failure.detail, failure.copyOutput, '', null));
      return;
    }
    dispatch(dismissTerminalStatus());
    dispatch(
      showNotification(
        'success',
        `Opened ${label} on the host worktree for ${selection.tenant} / ${selection.environment}.`,
      ),
    );
  };

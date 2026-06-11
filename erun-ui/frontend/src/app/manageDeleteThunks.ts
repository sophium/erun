import type { DeleteEnvironmentResult } from '@/types';

import { environmentApi } from './api/environmentApi';
import { reloadStateAfterEnvironmentChange } from './bootThunks';
import { readError } from './errors';
import { closeManageDialog } from './manageDialogThunks';
import { showTerminalMessage } from './notificationThunks';
import { patchManageDialog, setManageDialog } from './slices/manageDialogSlice';
import { setSelected } from './slices/selectionSlice';
import { setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import { defaultManageDialog } from './state';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { deleteConfirmationValue, normalizeDialogValue, selectionKey } from './versionSuggestions';

export const submitManageDelete =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const dialog = getState().manageDialog;
    if (dialog.busy) {
      return;
    }
    const selection = dialog.selection;
    if (!selection) {
      dispatch(closeManageDialog());
      return;
    }
    const confirmation = normalizeDialogValue(dialog.confirmation);
    const expected = deleteConfirmationValue(selection);
    if (confirmation !== expected) {
      return;
    }

    dispatch(patchManageDialog({ busy: true, busyAction: 'delete', busyTarget: '', error: '' }));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`));

    try {
      const result = (await dispatch(
        environmentApi.endpoints.deleteEnvironment.initiate({ selection, confirmation }),
      ).unwrap()) as DeleteEnvironmentResult;
      const currentSelected = getState().selection.selected;
      const deletedSelected = currentSelected
        ? selectionKey(currentSelected) === selectionKey(selection)
        : false;
      if (deletedSelected) {
        dispatch(setSelected(null));
        dispatch(setSessionId(0));
        controller.resetTerminal();
      }
      await dispatch(reloadStateAfterEnvironmentChange());
      dispatch(setManageDialog(defaultManageDialog()));
      dispatch(setTerminalCopyOutput(''));
      dispatch(setTerminalCopyStatus(''));
      const warnings = [
        result.namespaceDeleteError
          ? `Namespace deletion failed: ${result.namespaceDeleteError}`
          : '',
        result.cloudContextStopError
          ? `Cloud context stop failed: ${result.cloudContextStopError}`
          : '',
      ]
        .filter(Boolean)
        .join(' ');
      const warning = warnings ? ` ${warnings}` : '';
      dispatch(showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`));
    } catch (error) {
      const message = readError(error);
      dispatch(
        patchManageDialog({
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: message,
        }),
      );
      dispatch(
        setTerminalCopyOutput(
          `Failed to delete ${selection.tenant} / ${selection.environment}: ${message}`,
        ),
      );
      dispatch(setTerminalCopyStatus(''));
      dispatch(showTerminalMessage(message));
    }
  };

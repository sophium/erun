import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { showTerminalError, showTerminalMessage } from './notificationThunks';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// submitManageStop keeps the dialog open so freed capacity shows immediately.
// There is no Start: opening the environment wakes it.
export const submitManageStop = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  if (dialog.busy) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    return;
  }

  dispatch(patchManageDialog({ busy: true, busyAction: 'stop', busyTarget: '', error: '' }));
  dispatch(showTerminalMessage(`Stopping ${selection.tenant} / ${selection.environment}...`));
  try {
    const result = await dispatch(
      environmentApi.endpoints.stopEnvironment.initiate(selection),
    ).unwrap();
    dispatch(patchManageDialog({ busy: false, busyAction: '', busyTarget: '', error: '' }));
    dispatch(showTerminalMessage(stopEnvironmentMessage(result)));
  } catch (error) {
    const message = readError(error);
    dispatch(patchManageDialog({ busy: false, busyAction: '', busyTarget: '', error: message }));
    dispatch(showTerminalError(message));
  }
};

// stopEnvironmentMessage names the way back and the components still running,
// since a pod outliving a stop reads as a failed stop.
function stopEnvironmentMessage(result: UIEnvironmentStopResult): string {
  const name = `${result.tenant} / ${result.environment}`;
  const kept = stopEnvironmentKeptComponents(result.remainingComponents);
  const keptClause =
    kept === '' ? '' : ` Platform component(s) keep running and keep holding capacity: ${kept}.`;
  if (result.alreadyStopped) {
    return `${name} was already stopped.${keptClause} Click it in the sidebar to start it again.`;
  }
  return `Stopped ${name} and returned its runtime's capacity to the node.${keptClause} Click it in the sidebar to start it again.`;
}

// stopEnvironmentKeptComponents is '' when the environment deploys no components.
export function stopEnvironmentKeptComponents(components: string[] | undefined): string {
  return (components ?? [])
    .map((name) => name.trim())
    .filter((name) => name !== '')
    .join(', ');
}

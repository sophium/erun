import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// submitManageStop stops the environment's runtime from the Runtime tab, next
// to Deploy: the operator is already looking at the resource sliders when they
// discover there is no headroom left, and stopping an idle environment is what
// creates it. The dialog stays open so the freed capacity is visible in the
// refreshed "Available for this runtime" line right away.
//
// There is no matching Start action: opening the environment is what wakes it
// (`erun open` scales the runtime back up), so a second wake path would be a
// second implementation of the same thing.
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
    dispatch(showTerminalMessage(message));
  }
};

// stopEnvironmentMessage always names the way back, so a stopped environment is
// never a dead end the operator has to work out for themselves.
function stopEnvironmentMessage(result: UIEnvironmentStopResult): string {
  const name = `${result.tenant} / ${result.environment}`;
  if (result.alreadyStopped) {
    return `${name} was already stopped. Click it in the sidebar to start it again.`;
  }
  return `Stopped ${name} and returned its capacity to the node. Click it in the sidebar to start it again.`;
}

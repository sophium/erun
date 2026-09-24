import type { UIEnvironmentStopResult } from '@/uiLifecycleTypes';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { showTerminalError, showTerminalMessage } from './notificationThunks';
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
    dispatch(showTerminalError(message));
  }
};

// stopEnvironmentMessage always names the way back, so a stopped environment is
// never a dead end the operator has to work out for themselves.
//
// It also names the platform components the stop left running, on both
// outcomes. A stop scales the runtime Deployment and nothing else, so those
// component pods are still standing afterwards — and a pod that outlives a stop
// with no explanation reads as the stop having failed. On the already-stopped
// outcome they are the entire explanation for why nothing changed.
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

// stopEnvironmentKeptComponents names the platform components a stop leaves
// holding their capacity, or '' when the environment deploys none: a
// runtime-only environment has nothing to explain, and a sentence saying
// "nothing else is left running" would spend the operator's attention on a fact
// they never had to wonder about. Shared with the Runtime tab's Stop control,
// which says the same thing before the click rather than after it.
export function stopEnvironmentKeptComponents(components: string[] | undefined): string {
  return (components ?? [])
    .map((name) => name.trim())
    .filter((name) => name !== '')
    .join(', ');
}

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { submitManageDeploy } from './manageDeployThunks';
import { setManageTab } from './manageDialogThunks';
import { showTerminalMessage } from './notificationThunks';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// checkManageEnvironmentHealth runs the out-of-pod "Check environment"
// diagnostics for the managed env and stores the result on the dialog. It is an
// explicit user action (never implicit on open) and side-effect-free — it only
// reads config and queries the cluster.
export const checkManageEnvironmentHealth =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    if (dialog.busy || dialog.configLoading || dialog.healthLoading || !selection) {
      return;
    }
    dispatch(patchManageDialog({ healthLoading: true, error: '' }));
    try {
      const result = await dispatch(
        environmentApi.endpoints.checkEnvironmentHealth.initiate(selection),
      ).unwrap();
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({ health: result, healthLoading: false }));
    } catch (error) {
      if (!getState().manageDialog.open) {
        return;
      }
      const message = readError(error);
      dispatch(patchManageDialog({ healthLoading: false, error: message }));
      dispatch(showTerminalMessage(message));
    }
  };

// deployFromHealthCheck is the "runtime not deployed" recovery: it triggers the
// same deploy flow the pending-redeploy banner uses (build→push→deploy for a
// local-agent env, deploy for a runtime env), closing the dialog so the deploy's
// progress surfaces through the activity queue.
export const deployFromHealthCheck = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(submitManageDeploy());
};

// focusRegistryFieldFromHealthCheck is the "no registry" recovery: it moves the
// operator to the General tab's Container registries editor so they can add one
// (recognition over recall — the fix is a named jump to the right control).
export const focusRegistryFieldFromHealthCheck = (): AppThunk => (dispatch) => {
  dispatch(setManageTab('general'));
  window.setTimeout(() => {
    const field = document.getElementById('environment-config-registry-0');
    if (field instanceof HTMLElement) {
      field.scrollIntoView({ block: 'center' });
      field.focus();
      return;
    }
    document.getElementById('environment-config-add-registry')?.focus();
  }, 0);
};

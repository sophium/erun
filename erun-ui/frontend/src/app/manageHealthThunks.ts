import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { submitManageDeploy } from './manageDeployThunks';
import { setManageTab } from './manageDialogThunks';
import { showTerminalError } from './notificationThunks';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';

// checkManageEnvironmentHealth is read-only and runs only on explicit request.
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
      dispatch(showTerminalError(message));
    }
  };

// deployFromHealthCheck closes the dialog so progress shows in the activity queue.
export const deployFromHealthCheck = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(submitManageDeploy());
};

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

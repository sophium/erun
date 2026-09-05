import type { UICloudContextStatus } from '@/types';

import { cloudApi } from './api/cloudApi';
import { refreshKubernetesContexts } from './dialogContextsThunks';
import { readError } from './errors';
import { showTerminalError, showTerminalMessage } from './notificationThunks';
import { patchManageDialog } from './slices/manageDialogSlice';
import type { AppThunk } from './store';
import { normalizeDialogValue } from './versionSuggestions';

export const startManageCloudContext =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updateManageCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
        'Started',
      ),
    );
    void dispatch(refreshKubernetesContexts());
  };

export const stopManageCloudContext =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updateManageCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
        'Stopped',
      ),
    );
  };

const updateManageCloudContextPower =
  (
    name: string,
    action: (name: string) => Promise<unknown>,
    label: string,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const contextName = normalizeDialogValue(name);
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading || !dialog.selection || !contextName) {
      return;
    }
    dispatch(
      patchManageDialog({
        busy: true,
        busyAction: 'cloud-context-power',
        busyTarget: contextName,
        error: '',
      }),
    );
    try {
      const context = (await action(contextName)) as UICloudContextStatus;
      const currentConfig = getState().manageDialog.config;
      dispatch(
        patchManageDialog({
          config: { ...currentConfig, cloudContext: context },
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(
        showTerminalMessage(`${label} cloud context ${context.kubernetesContext || context.name}.`),
      );
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
      dispatch(showTerminalError(message));
    }
  };

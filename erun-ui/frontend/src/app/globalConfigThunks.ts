import type { UICloudContextInitInput, UIERunConfig } from '@/types';

import { globalConfigApi } from './api/globalConfigApi';
import { cloudContextDraftForConfig } from './cloudContextState';
import { readError } from './errors';
import { showNotification, showTerminalError } from './notificationThunks';
import { patchGlobalConfigDialog, setGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import type { GlobalConfigDialogState } from './state';
import { defaultCloudContextInitInput, defaultGlobalConfigDialog } from './state';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

// The controller is reserved for imperative xterm/PTY work; thunks otherwise
// operate purely on Redux state.

// Cloud provider/context thunks live in ./globalConfigCloudThunks to keep
// this file under eslint's max-lines; re-exported so no call site changes.
export * from './globalConfigCloudThunks';

export const openGlobalConfigDialog = (): AppThunk => (dispatch) => {
  dispatch(
    setGlobalConfigDialog({
      open: true,
      config: {
        defaultTenant: '',
        cloudProviders: [],
        cloudContexts: [],
      },
      cloudContextDraft: defaultCloudContextInitInput(),
      erunApiUrlDraft: '',
      configLoading: true,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    }),
  );
  void dispatch(loadGlobalConfig());
};

export const closeGlobalConfigDialog = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  if (getState().globalConfigDialog.busy) {
    return;
  }
  dispatch(setGlobalConfigDialog(defaultGlobalConfigDialog()));
  controller.focusTerminalSoon();
};

export const updateGlobalConfigDialog =
  (values: Partial<GlobalConfigDialogState>): AppThunk =>
  (dispatch, getState) => {
    if (getState().globalConfigDialog.busy) {
      return;
    }
    dispatch(patchGlobalConfigDialog({ ...values, error: values.error ?? '' }));
  };

export const updateGlobalConfig =
  (values: Partial<UIERunConfig>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      updateGlobalConfigDialog({
        config: {
          ...dialog.config,
          ...values,
        },
      }),
    );
  };

export const updateCloudContextDraft =
  (values: Partial<UICloudContextInitInput>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      updateGlobalConfigDialog({
        cloudContextDraft: {
          ...dialog.cloudContextDraft,
          ...values,
        },
      }),
    );
  };

export const loadGlobalConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (!dialog.open) {
    return;
  }
  dispatch(patchGlobalConfigDialog({ configLoading: true, error: '' }));
  try {
    const result = await dispatch(
      globalConfigApi.endpoints.getERunConfig.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    const currentDraft = getState().globalConfigDialog.cloudContextDraft;
    dispatch(
      patchGlobalConfigDialog({
        config: result,
        cloudContextDraft: cloudContextDraftForConfig(result, currentDraft),
        configLoading: false,
        error: '',
      }),
    );
  } catch (error) {
    dispatch(
      patchGlobalConfigDialog({
        configLoading: false,
        error: readError(error),
      }),
    );
  }
};

export const submitGlobalConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  dispatch(patchGlobalConfigDialog({ busy: true, busyAction: 'save', busyTarget: '', error: '' }));
  try {
    const result = await dispatch(
      globalConfigApi.endpoints.saveERunConfig.initiate(dialog.config),
    ).unwrap();
    dispatch(
      patchGlobalConfigDialog({
        config: result,
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      }),
    );
    dispatch(showNotification('success', 'Saved ERun config.'));
    dispatch(closeGlobalConfigDialog());
  } catch (error) {
    const message = readError(error);
    dispatch(
      patchGlobalConfigDialog({
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: message,
      }),
    );
    dispatch(showTerminalError(message));
  }
};

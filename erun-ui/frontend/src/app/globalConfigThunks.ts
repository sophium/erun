import type {
  StartSessionResult,
  UICloudContextInitInput,
  UICloudContextStatus,
  UIERunConfig,
  UIIdleStatus,
} from '@/types';

import { StartCloudInitAWSSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { globalConfigApi } from './api/globalConfigApi';
import {
  cloudContextDraftForConfig,
  idleCloudContextAction,
  replaceCloudContext,
  replaceCloudProvider,
} from './cloudContextState';
import { refreshKubernetesContexts } from './dialogContextsThunks';
import { readError } from './errors';
import { refreshIdleStatus } from './idleThunks';
import { hideTerminalMessage, showNotification, showTerminalMessage } from './notificationThunks';
import { openSelection } from './sessionThunks';
import { patchGlobalConfigDialog, setGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import { setIdleCloudContextBusy, setIdleStatus } from './slices/idleSlice';
import { trackCloudInitSession } from './slices/sessionsSlice';
import { setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import type { GlobalConfigDialogState } from './state';
import { defaultCloudContextInitInput, defaultGlobalConfigDialog } from './state';
import type { AppDispatch, AppThunk, RootState } from './store';
import { requireController } from './thunkExtra';

// Each thunk reads from the Redux store via getState() and writes via
// dispatch(); the controller is only used for imperative xterm/PTY work
// (refreshKubernetesContexts, fitTerminal, sessions, etc.).

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

export const refreshCloudProviders = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (!dialog.open || dialog.busy) {
    return;
  }
  try {
    const cloudProviders = await dispatch(
      cloudApi.endpoints.getCloudProviderStatuses.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    const currentConfig = getState().globalConfigDialog.config;
    dispatch(
      patchGlobalConfigDialog({
        config: { ...currentConfig, cloudProviders },
        error: '',
      }),
    );
    dispatch(showNotification('success', 'Cloud aliases refreshed.'));
  } catch (error) {
    const message = readError(error);
    dispatch(patchGlobalConfigDialog({ error: message }));
    dispatch(showTerminalMessage(message));
  }
};

export const refreshCloudContexts = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (!dialog.open || dialog.busy) {
    return;
  }
  try {
    const cloudContexts = await dispatch(
      cloudApi.endpoints.getCloudContextStatuses.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    const currentConfig = getState().globalConfigDialog.config;
    dispatch(
      patchGlobalConfigDialog({
        config: { ...currentConfig, cloudContexts },
        error: '',
      }),
    );
    dispatch(showNotification('success', 'Cloud contexts refreshed.'));
  } catch (error) {
    const message = readError(error);
    dispatch(patchGlobalConfigDialog({ error: message }));
    dispatch(showTerminalMessage(message));
  }
};

export const initGlobalCloudContext = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  dispatch(
    patchGlobalConfigDialog({
      busy: true,
      busyAction: 'cloud-context-init',
      busyTarget: '',
      error: '',
    }),
  );
  try {
    const context = await dispatch(
      cloudApi.endpoints.initCloudContext.initiate(dialog.cloudContextDraft),
    ).unwrap();
    const currentConfig = getState().globalConfigDialog.config;
    dispatch(
      patchGlobalConfigDialog({
        config: {
          ...currentConfig,
          cloudContexts: replaceCloudContext(currentConfig.cloudContexts ?? [], context),
        },
        cloudContextDraft: cloudContextDraftForConfig(currentConfig, {
          ...defaultCloudContextInitInput(),
          cloudProviderAlias: dialog.cloudContextDraft.cloudProviderAlias,
          region: dialog.cloudContextDraft.region,
        }),
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
      }),
    );
    dispatch(showTerminalMessage(`Initialized cloud context ${context.kubernetesContext}.`));
    void dispatch(refreshKubernetesContexts());
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
    dispatch(showTerminalMessage(message));
  }
};

export const stopGlobalCloudContext =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updateCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
        'Stopped',
      ),
    );
  };

export const startGlobalCloudContext =
  (name: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updateCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
        'Started',
      ),
    );
    void dispatch(refreshKubernetesContexts());
  };

export const toggleIdleCloudContext = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const action = idleCloudContextAction(state.idle.idleStatus, state.idle.idleCloudContextBusy);
  if (!action) {
    return;
  }
  const selection = state.selection.selected ? { ...state.selection.selected } : null;
  dispatch(setIdleCloudContextBusy(true));
  try {
    const context = (await action.run(action.name)) as UICloudContextStatus;
    applyIdleCloudContextResult(dispatch, getState, action.idleStatus, context);
    dispatch(setIdleCloudContextBusy(false));
    dispatch(
      showNotification(
        'success',
        `${action.label} cloud environment ${context.kubernetesContext || context.name}.`,
      ),
    );
    if (action.refreshKubernetesContexts) {
      void dispatch(refreshKubernetesContexts());
    }
    if (action.operation === 'start' && selection) {
      await dispatch(openSelection(selection));
    }
    void dispatch(refreshIdleStatus());
  } catch (error) {
    const message = readError(error);
    dispatch(setIdleCloudContextBusy(false));
    dispatch(showNotification('error', message));
    dispatch(showTerminalMessage(message));
  }
};

export const startAWSCloudInit =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction: 'cloud-provider-init',
        busyTarget: '',
        error: '',
      }),
    );
    try {
      controller.fitTerminal();
      const size = controller.terminalSize();
      const result = (await StartCloudInitAWSSession(size.cols, size.rows)) as StartSessionResult;
      dispatch(trackCloudInitSession(result.sessionId));
      dispatch(setGlobalConfigDialog(defaultGlobalConfigDialog()));
      dispatch(setSessionId(result.sessionId));
      dispatch(setTerminalCopyOutput(''));
      dispatch(setTerminalCopyStatus(''));
      controller.resetTerminal();
      dispatch(hideTerminalMessage());
      controller.focusTerminalSoon();
      controller.queueTerminalResize();
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
      dispatch(showTerminalMessage(message));
    }
  };

export const loginGlobalCloudProvider =
  (alias: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction: 'cloud-provider-login',
        busyTarget: alias,
        error: '',
      }),
    );
    try {
      const provider = await dispatch(
        cloudApi.endpoints.loginCloudProvider.initiate(alias),
      ).unwrap();
      const currentConfig = getState().globalConfigDialog.config;
      dispatch(
        patchGlobalConfigDialog({
          config: {
            ...currentConfig,
            cloudProviders: replaceCloudProvider(currentConfig.cloudProviders ?? [], provider),
          },
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(showTerminalMessage(`${provider.alias}: ${provider.status}`));
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
      dispatch(showTerminalMessage(message));
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
    dispatch(showTerminalMessage(message));
  }
};

function applyIdleCloudContextResult(
  dispatch: AppDispatch,
  getState: () => RootState,
  idleStatusFallback: UIIdleStatus,
  context: UICloudContextStatus,
): void {
  const current = getState().idle.idleStatus;
  dispatch(
    setIdleStatus({
      ...(current ?? idleStatusFallback),
      cloudContextName: context.name,
      cloudContextStatus: context.status,
      cloudContextLabel: context.kubernetesContext || context.name,
    }),
  );
  const globalDialog = getState().globalConfigDialog;
  if (!globalDialog.open) {
    return;
  }
  dispatch(
    patchGlobalConfigDialog({
      config: {
        ...globalDialog.config,
        cloudContexts: replaceCloudContext(globalDialog.config.cloudContexts ?? [], context),
      },
    }),
  );
}

const updateCloudContextPower =
  (
    name: string,
    action: (name: string) => Promise<unknown>,
    label: string,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction: 'cloud-context-power',
        busyTarget: name,
        error: '',
      }),
    );
    try {
      const context = (await action(name)) as UICloudContextStatus;
      const currentConfig = getState().globalConfigDialog.config;
      dispatch(
        patchGlobalConfigDialog({
          config: {
            ...currentConfig,
            cloudContexts: replaceCloudContext(currentConfig.cloudContexts ?? [], context),
          },
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(showTerminalMessage(`${label} cloud context ${context.kubernetesContext}.`));
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
      dispatch(showTerminalMessage(message));
    }
  };

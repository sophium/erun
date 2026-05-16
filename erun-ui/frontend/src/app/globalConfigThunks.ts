import { StartCloudInitAWSSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { globalConfigApi } from './api/globalConfigApi';
import {
  cloudContextDraftForConfig,
  idleCloudContextAction,
  replaceCloudContext,
  replaceCloudProvider,
} from './cloudContextState';
import { readError } from './errors';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalMessage,
} from './notificationThunks';
import type { AppState, GlobalConfigDialogState } from './state';
import { defaultCloudContextInitInput, defaultGlobalConfigDialog } from './state';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import type {
  StartSessionResult,
  UICloudContextInitInput,
  UICloudContextStatus,
  UIERunConfig,
} from '@/types';

// Each thunk takes the same imperative dependencies the GlobalConfigWorkflow
// class held in deps; it reaches them through requireController(extra), which
// returns the ERunUIController set in its constructor. State reads and writes
// still go through controller.state, the Proxy that dispatches matching
// slice actions on write.

export const openGlobalConfigDialog = (): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.globalConfigDialog = {
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
  };
  void dispatch(loadGlobalConfig());
};

export const closeGlobalConfigDialog = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.globalConfigDialog.busy) {
    return;
  }
  controller.state.globalConfigDialog = defaultGlobalConfigDialog();
  controller.focusTerminalSoon();
};

export const updateGlobalConfigDialog = (
  values: Partial<GlobalConfigDialogState>,
): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.globalConfigDialog.busy) {
    return;
  }
  controller.state.globalConfigDialog = {
    ...controller.state.globalConfigDialog,
    ...values,
    error: values.error ?? '',
  };
};

export const updateGlobalConfig = (
  values: Partial<UIERunConfig>,
): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.globalConfigDialog.busy || controller.state.globalConfigDialog.configLoading) {
    return;
  }
  dispatch(
    updateGlobalConfigDialog({
      config: {
        ...controller.state.globalConfigDialog.config,
        ...values,
      },
    }),
  );
};

export const updateCloudContextDraft = (
  values: Partial<UICloudContextInitInput>,
): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.globalConfigDialog.busy || controller.state.globalConfigDialog.configLoading) {
    return;
  }
  dispatch(
    updateGlobalConfigDialog({
      cloudContextDraft: {
        ...controller.state.globalConfigDialog.cloudContextDraft,
        ...values,
      },
    }),
  );
};

export const loadGlobalConfig = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (!dialog.open) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, configLoading: true, error: '' };
  try {
    const result = await dispatch(
      globalConfigApi.endpoints.getERunConfig.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: result,
      cloudContextDraft: cloudContextDraftForConfig(result, controller.state.globalConfigDialog.cloudContextDraft),
      configLoading: false,
      error: '',
    };
  } catch (error) {
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      configLoading: false,
      error: readError(error),
    };
  }
};

export const refreshCloudProviders = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (!dialog.open || dialog.busy) {
    return;
  }
  try {
    const cloudProviders = await dispatch(
      cloudApi.endpoints.getCloudProviderStatuses.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: { ...controller.state.globalConfigDialog.config, cloudProviders },
      error: '',
    };
    dispatch(showNotification('success', 'Cloud aliases refreshed.'));
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = { ...controller.state.globalConfigDialog, error: message };
    dispatch(showTerminalMessage(message));
  }
};

export const refreshCloudContexts = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (!dialog.open || dialog.busy) {
    return;
  }
  try {
    const cloudContexts = await dispatch(
      cloudApi.endpoints.getCloudContextStatuses.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: { ...controller.state.globalConfigDialog.config, cloudContexts },
      error: '',
    };
    dispatch(showNotification('success', 'Cloud contexts refreshed.'));
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = { ...controller.state.globalConfigDialog, error: message };
    dispatch(showTerminalMessage(message));
  }
};

export const initGlobalCloudContext = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-context-init', busyTarget: '', error: '' };
  try {
    const context = await dispatch(cloudApi.endpoints.initCloudContext.initiate(dialog.cloudContextDraft)).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: {
        ...controller.state.globalConfigDialog.config,
        cloudContexts: replaceCloudContext(controller.state.globalConfigDialog.config.cloudContexts || [], context),
      },
      cloudContextDraft: cloudContextDraftForConfig(controller.state.globalConfigDialog.config, {
        ...defaultCloudContextInitInput(),
        cloudProviderAlias: dialog.cloudContextDraft.cloudProviderAlias,
        region: dialog.cloudContextDraft.region,
      }),
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    dispatch(showTerminalMessage(`Initialized cloud context ${context.kubernetesContext}.`));
    void controller.refreshKubernetesContexts();
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    dispatch(showTerminalMessage(message));
  }
};

export const stopGlobalCloudContext = (name: string): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  await dispatch(
    updateCloudContextPower(
      name,
      (target) => dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
      'Stopped',
    ),
  );
  void requireController(extra);
};

export const startGlobalCloudContext = (name: string): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  await dispatch(
    updateCloudContextPower(
      name,
      (target) => dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
      'Started',
    ),
  );
  void requireController(extra).refreshKubernetesContexts();
};

export const toggleIdleCloudContext = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const action = idleCloudContextAction(controller.state.idleStatus, controller.state.idleCloudContextBusy);
  if (!action) {
    return;
  }
  const selection = controller.state.selected ? { ...controller.state.selected } : null;
  controller.state.idleCloudContextBusy = true;
  try {
    const context = (await action.run(action.name)) as UICloudContextStatus;
    applyIdleCloudContextResult(controller, action.idleStatus, context);
    controller.state.idleCloudContextBusy = false;
    dispatch(showNotification('success', `${action.label} cloud environment ${context.kubernetesContext || context.name}.`));
    if (action.refreshKubernetesContexts) {
      void controller.refreshKubernetesContexts();
    }
    if (action.operation === 'start' && selection) {
      await controller.openSelection(selection);
    }
    void controller.refreshIdleStatus();
  } catch (error) {
    const message = readError(error);
    controller.state.idleCloudContextBusy = false;
    dispatch(showNotification('error', message));
    dispatch(showTerminalMessage(message));
  }
};

export const startAWSCloudInit = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-init', busyTarget: '', error: '' };
  try {
    controller.fitTerminal();
    const size = controller.terminalSize();
    const result = (await StartCloudInitAWSSession(size.cols, size.rows)) as StartSessionResult;
    controller.sessions.trackCloudInitSession(result.sessionId);
    controller.state.globalConfigDialog = defaultGlobalConfigDialog();
    controller.state.sessionId = result.sessionId;
    controller.state.terminalCopyOutput = '';
    controller.state.terminalCopyStatus = '';
    controller.resetTerminal();
    dispatch(hideTerminalMessage());
    controller.focusTerminalSoon();
    controller.queueTerminalResize();
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    dispatch(showTerminalMessage(message));
  }
};

export const loginGlobalCloudProvider = (alias: string): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-provider-login', busyTarget: alias, error: '' };
  try {
    const provider = await dispatch(cloudApi.endpoints.loginCloudProvider.initiate(alias)).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: {
        ...controller.state.globalConfigDialog.config,
        cloudProviders: replaceCloudProvider(controller.state.globalConfigDialog.config.cloudProviders || [], provider),
      },
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    dispatch(showTerminalMessage(`${provider.alias}: ${provider.status}`));
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    dispatch(showTerminalMessage(message));
  }
};

export const submitGlobalConfig = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
  try {
    const result = await dispatch(globalConfigApi.endpoints.saveERunConfig.initiate(dialog.config)).unwrap();
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: result,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    dispatch(showNotification('success', 'Saved ERun config.'));
    dispatch(closeGlobalConfigDialog());
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    dispatch(showTerminalMessage(message));
  }
};

function applyIdleCloudContextResult(
  controller: NonNullable<ReturnType<typeof requireController>>,
  idleStatus: NonNullable<AppState['idleStatus']>,
  context: UICloudContextStatus,
): void {
  controller.state.idleStatus = {
    ...(controller.state.idleStatus ?? idleStatus),
    cloudContextName: context.name,
    cloudContextStatus: context.status,
    cloudContextLabel: context.kubernetesContext || context.name,
  };
  if (!controller.state.globalConfigDialog.open) {
    return;
  }
  controller.state.globalConfigDialog = {
    ...controller.state.globalConfigDialog,
    config: {
      ...controller.state.globalConfigDialog.config,
      cloudContexts: replaceCloudContext(controller.state.globalConfigDialog.config.cloudContexts || [], context),
    },
  };
}

const updateCloudContextPower = (
  name: string,
  action: (name: string) => Promise<unknown>,
  label: string,
): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.globalConfigDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  controller.state.globalConfigDialog = { ...dialog, busy: true, busyAction: 'cloud-context-power', busyTarget: name, error: '' };
  try {
    const context = (await action(name)) as UICloudContextStatus;
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      config: {
        ...controller.state.globalConfigDialog.config,
        cloudContexts: replaceCloudContext(controller.state.globalConfigDialog.config.cloudContexts || [], context),
      },
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    dispatch(showTerminalMessage(`${label} cloud context ${context.kubernetesContext}.`));
  } catch (error) {
    const message = readError(error);
    controller.state.globalConfigDialog = {
      ...controller.state.globalConfigDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    dispatch(showTerminalMessage(message));
  }
};

import type { StartSessionResult, UICloudContextStatus, UIIdleStatus } from '@/types';

import {
  StartCloudInitAWSSession,
  StartCloudInitCloudflareSession,
} from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { tenantApi } from './api/tenantApi';
import {
  cloudContextDraftForConfig,
  idleCloudContextAction,
  replaceCloudContext,
  replaceCloudProvider,
} from './cloudContextState';
import { cloudNodeOperationFor } from './cloudNodeOperations';
import { refreshKubernetesContexts } from './dialogContextsThunks';
import { readError } from './errors';
import { refreshIdleStatus } from './idleThunks';
import type { CloudInitProvider } from './model';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalError,
  showTerminalMessage,
} from './notificationThunks';
import { openSelection } from './sessionThunks';
import { patchGlobalConfigDialog, setGlobalConfigDialog } from './slices/globalConfigDialogSlice';
import {
  finishCloudNodeOperation,
  setIdleStatus,
  startCloudNodeOperation,
} from './slices/idleSlice';
import { trackCloudInitSession } from './slices/sessionsSlice';
import { setSessionId } from './slices/terminalSlice';
import { setTerminalCopyOutput, setTerminalCopyStatus } from './slices/terminalStatusSlice';
import { defaultCloudContextInitInput, defaultGlobalConfigDialog } from './state';
import type { AppDispatch, AppThunk, RootState } from './store';
import { requireController } from './thunkExtra';

// The controller is reserved for imperative xterm/PTY work; thunks otherwise
// operate purely on Redux state.

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
    dispatch(showTerminalError(message));
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
    dispatch(showTerminalError(message));
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
    dispatch(showTerminalError(message));
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
  const action = idleCloudContextAction(
    state.idle.idleStatus,
    cloudNodeOperationFor(state.idle.cloudNodeOperations, state.idle.idleStatus?.cloudContextName),
  );
  if (!action) {
    return;
  }
  const selection = state.selection.selected ? { ...state.selection.selected } : null;
  // Recorded against the node, not as a bare "something is busy": the titlebar
  // renders the progressive label only for the node its own name refers to.
  dispatch(startCloudNodeOperation({ name: action.name, operation: action.operation }));
  try {
    const context = (await action.run(action.name)) as UICloudContextStatus;
    applyIdleCloudContextResult(dispatch, getState, action.idleStatus, context);
    dispatch(finishCloudNodeOperation({ name: action.name }));
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
    dispatch(finishCloudNodeOperation({ name: action.name }));
    dispatch(showNotification('error', message));
    dispatch(showTerminalError(message));
  }
};

// Adding a cloud alias is delegated entirely to the CLI (prompt, verify,
// resolve, persist); the desktop only hands the terminal over to it.
const startCloudInitSession =
  (
    busyAction: 'cloud-provider-init' | 'cloud-provider-cloudflare-init',
    provider: CloudInitProvider,
    startSession: (cols: number, rows: number) => Promise<unknown>,
  ): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const dialog = getState().globalConfigDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction,
        busyTarget: '',
        error: '',
      }),
    );
    try {
      controller.fitTerminal();
      const size = controller.terminalSize();
      const result = (await startSession(size.cols, size.rows)) as StartSessionResult;
      dispatch(trackCloudInitSession({ sessionId: result.sessionId, provider }));
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
      dispatch(showTerminalError(message));
    }
  };

export const startAWSCloudInit = (): AppThunk<Promise<void>> =>
  startCloudInitSession('cloud-provider-init', 'aws', StartCloudInitAWSSession);

export const startCloudflareCloudInit = (): AppThunk<Promise<void>> =>
  startCloudInitSession(
    'cloud-provider-cloudflare-init',
    'cloudflare',
    StartCloudInitCloudflareSession,
  );

// startERunCloudInit reaches the exact code path the tenant dashboard's
// Connect panel already uses (tenantApi's connectERunPlatform, which attaches
// the alias with no sign-in of its own), then signs in through
// loginGlobalCloudProvider the same way an existing alias row's own Login
// button does — so settings never grows a second, sign-in-less way to create
// an erun alias.
export const startERunCloudInit =
  (apiUrl: string): AppThunk<Promise<boolean>> =>
  async (dispatch, getState) => {
    const trimmed = apiUrl.trim();
    const dialog = getState().globalConfigDialog;
    if (!trimmed || dialog.busy || dialog.configLoading) {
      return false;
    }
    dispatch(
      patchGlobalConfigDialog({
        busy: true,
        busyAction: 'cloud-provider-erun-init',
        busyTarget: '',
        error: '',
      }),
    );
    try {
      const provider = await dispatch(
        tenantApi.endpoints.connectERunPlatform.initiate({ apiUrl: trimmed }),
      ).unwrap();
      const currentConfig = getState().globalConfigDialog.config;
      dispatch(
        patchGlobalConfigDialog({
          config: {
            ...currentConfig,
            cloudProviders: replaceCloudProvider(currentConfig.cloudProviders ?? [], provider),
          },
          erunApiUrlDraft: '',
          busy: false,
          busyAction: '',
          busyTarget: '',
          error: '',
        }),
      );
      dispatch(
        showTerminalMessage(`Connected erun platform alias ${provider.alias}. Signing in...`),
      );
      void dispatch(loginGlobalCloudProvider(provider.alias));
      return true;
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
      return false;
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
      dispatch(showTerminalError(message));
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
      dispatch(showTerminalError(message));
    }
  };

import { StartDoctorSession, StartSSHDInitSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { environmentApi } from './api/environmentApi';
import { applyPendingDebugHeader, setPendingDebugHeader, syncDebugDisplay } from './debugThunks';
import { readError } from './errors';
import type { HiddenSessionMode } from './model';
import { showTerminalMessage } from './notificationThunks';
import { runtimePodConfigToDisplay, runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
import { selectManageRuntimeImage } from './selectors';
import {
  patchManageDialog,
  setManageDialog,
} from './slices/manageDialogSlice';
import { bumpManageDialogVersion } from './slices/requestCountersSlice';
import { setSelected } from './slices/selectionSlice';
import {
  registerDebugSession,
  trackDoctorSession,
  trackSSHDInitSession,
} from './slices/sessionsSlice';
import { setSessionId, setDebugOutput } from './slices/terminalSlice';
import { setVersionSuggestions } from './slices/tenantsSlice';
import {
  setTerminalCopyOutput,
  setTerminalCopyStatus,
} from './slices/terminalStatusSlice';
import {
  defaultEnvironmentConfig,
  defaultManageDialog,
  type ManageDialogState,
} from './state';
import { rememberPastContainerRegistry } from './storage';
import type { AppThunk } from './store';
import { formatDebugCommand, hiddenSessionBusyMessage } from './terminalStatus';
import { requireController } from './thunkExtra';
import {
  deleteConfirmationValue,
  normalizeDialogValue,
  normalizeVersionSuggestions,
  selectionKey,
} from './versionSuggestions';
import type {
  DeleteEnvironmentResult,
  ManageTab,
  StartSessionResult,
  UICloudContextStatus,
  UIEnvironmentConfig,
  UISelection,
  UIVersionSuggestion,
} from '@/types';

export const openManageDialog = (selection: UISelection): AppThunk => (dispatch) => {
  dispatch(setManageDialog({
    open: true,
    tab: 'general',
    selection,
    version: '',
    versionImage: '',
    config: { ...defaultEnvironmentConfig(), name: selection.environment },
    initialConfig: null,
    configLoading: true,
    resourceStatus: null,
    resourceStatusLoading: false,
    confirmation: '',
    busy: false,
    busyAction: '',
    busyTarget: '',
    choicesOpen: false,
    error: '',
    pendingRedeploy: false,
  }));
  void dispatch(refreshManageVersionSuggestions(false));
  void dispatch(loadManageConfig());
};

export const closeManageDialog = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  if (getState().manageDialog.busy) {
    return;
  }
  dispatch(setManageDialog(defaultManageDialog()));
  controller.focusTerminalSoon();
};

export const setManageTab = (tab: ManageTab): AppThunk => (dispatch, getState) => {
  if (getState().manageDialog.busy) {
    return;
  }
  dispatch(patchManageDialog({
    tab,
    choicesOpen: false,
    error: '',
  }));
};

export const updateManageDialog = (values: Partial<ManageDialogState>): AppThunk => (dispatch, getState) => {
  if (getState().manageDialog.busy) {
    return;
  }
  const versionReset = values.version !== undefined;
  dispatch(patchManageDialog({
    ...values,
    error: values.error ?? '',
    ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
  }));
};

export const toggleManageVersionChoices = (): AppThunk => (dispatch, getState) => {
  dispatch(setManageVersionChoicesOpen(!getState().manageDialog.choicesOpen));
};

export const setManageVersionChoicesOpen = (open: boolean): AppThunk => (dispatch, getState) => {
  const state = getState();
  if (state.manageDialog.busy) {
    return;
  }
  dispatch(patchManageDialog({
    choicesOpen: open && state.tenants.versionSuggestions.length > 0,
  }));
};

export const selectManageVersionSuggestion = (suggestion: UIVersionSuggestion | undefined): AppThunk =>
  (dispatch, getState) => {
    if (getState().manageDialog.busy) {
      return;
    }
    dispatch(patchManageDialog({
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    }));
  };

export const updateManageConfig = (values: Partial<UIEnvironmentConfig>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    const config = { ...dialog.config, ...values };
    if (values.cloudProviderAlias !== undefined) {
      config.cloudContext = undefined;
    }
    dispatch(patchManageDialog({ config, error: '' }));
  };

export const updateManageClaudeConfig = (values: Partial<UIEnvironmentConfig['claude']>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    const merged = { ...dialog.config.claude, ...values };
    const next: UIEnvironmentConfig['claude'] = {};
    if (merged.useMantle !== undefined) next.useMantle = merged.useMantle;
    if (merged.useBedrock !== undefined) next.useBedrock = merged.useBedrock;
    if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
    if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
    dispatch(patchManageDialog({
      config: { ...dialog.config, claude: next },
      error: '',
    }));
  };

export const updateManageSSHDConfig = (values: Partial<UIEnvironmentConfig['sshd']>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(patchManageDialog({
      config: {
        ...dialog.config,
        sshd: { ...dialog.config.sshd, ...values },
      },
      error: '',
    }));
  };

export const chooseWorkspaceSyncLocalFolder = (): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    if (dialog.busy || dialog.configLoading || !selection || !dialog.config.sshd.workspaceSyncEnabled) {
      return;
    }
    const folder = await dispatch(
      environmentApi.endpoints.chooseWorkspaceSyncLocalFolder.initiate({
        selection,
        current: dialog.config.sshd.workspaceSyncLocalPath || '',
      }),
    ).unwrap();
    const selected = String(folder || '').trim();
    if (!selected) {
      return;
    }
    dispatch(updateManageSSHDConfig({ workspaceSyncLocalPath: selected }));
  };

export const loadManageConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  const selection = dialog.selection;
  if (!dialog.open || !selection) {
    return;
  }
  dispatch(patchManageDialog({ configLoading: true, error: '' }));
  try {
    const result = await dispatch(
      environmentApi.endpoints.getEnvironmentConfig.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const displayConfig = { ...result, runtimePod: runtimePodConfigToDisplay(result.runtimePod) };
    dispatch(patchManageDialog({
      config: displayConfig,
      initialConfig: cloneEnvironmentConfig(displayConfig),
      configLoading: false,
      resourceStatusLoading: true,
      error: '',
    }));
    void dispatch(loadManageResourceStatus(result.kubernetesContext, selection));
  } catch (error) {
    dispatch(patchManageDialog({
      configLoading: false,
      error: readError(error),
    }));
  }
};

const loadManageResourceStatus = (kubernetesContext: string, selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    if (!getState().manageDialog.open) {
      return;
    }
    try {
      const status = await dispatch(
        environmentApi.endpoints.getRuntimeResourceStatus.initiate(
          { kubernetesContext, tenant: selection.tenant, environment: selection.environment },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({
        resourceStatus: status,
        resourceStatusLoading: false,
      }));
    } catch (error) {
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(patchManageDialog({
        resourceStatus: {
          kubernetesContext,
          available: false,
          message: readError(error),
          cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
          memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
        },
        resourceStatusLoading: false,
      }));
    }
  };

export const submitManageConfig = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const dialog = getState().manageDialog;
  if (dialog.busy || dialog.configLoading) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    dispatch(closeManageDialog());
    return;
  }
  const resourceError = runtimeResourceLimitMessage(dialog.config.runtimePod, dialog.resourceStatus);
  if (resourceError) {
    dispatch(patchManageDialog({ error: resourceError }));
    return;
  }
  dispatch(patchManageDialog({ busy: true, busyAction: 'save', busyTarget: '', error: '' }));
  try {
    const saveConfig = {
      ...dialog.config,
      runtimePod: runtimePodConfigToKubernetes(dialog.config.runtimePod),
    };
    const result = await dispatch(
      environmentApi.endpoints.saveEnvironmentConfig.initiate({ selection, config: saveConfig }),
    ).unwrap();
    rememberPastContainerRegistry(result.containerRegistry || saveConfig.containerRegistry);
    const displayConfig = { ...result, runtimePod: runtimePodConfigToDisplay(result.runtimePod) };
    dispatch(patchManageDialog({
      config: displayConfig,
      initialConfig: cloneEnvironmentConfig(displayConfig),
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
      pendingRedeploy: true,
    }));
  } catch (error) {
    const message = readError(error);
    dispatch(patchManageDialog({
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    }));
    dispatch(showTerminalMessage(message));
  }
};

export const startManageCloudContext = (name: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    await dispatch(
      updateManageCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.startCloudContext.initiate(target)).unwrap(),
        'Started',
      ),
    );
    void requireController(extra).refreshKubernetesContexts();
  };

export const stopManageCloudContext = (name: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    await dispatch(
      updateManageCloudContextPower(
        name,
        (target) => dispatch(cloudApi.endpoints.stopCloudContext.initiate(target)).unwrap(),
        'Stopped',
      ),
    );
  };

export const enableManageSSHD = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(startHiddenSession('sshd-init', StartSSHDInitSession));
};

export const startManageDoctor = (): AppThunk<Promise<void>> => async (dispatch) => {
  await dispatch(startHiddenSession('doctor', StartDoctorSession));
};

export const submitManageDeploy = (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
  const controller = requireController(extra);
  const dialog = getState().manageDialog;
  if (dialog.busy) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    dispatch(closeManageDialog());
    return;
  }
  const version = normalizeDialogValue(dialog.version);
  dispatch(closeManageDialog());
  await controller.startDeploySelection({
    ...selection,
    version,
    runtimeImage: version ? selectManageRuntimeImage(getState(), version) : '',
  });
};

export const submitManageDelete = (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
  const controller = requireController(extra);
  const dialog = getState().manageDialog;
  if (dialog.busy) {
    return;
  }
  const selection = dialog.selection;
  if (!selection) {
    dispatch(closeManageDialog());
    return;
  }
  const confirmation = normalizeDialogValue(dialog.confirmation);
  const expected = deleteConfirmationValue(selection);
  if (confirmation !== expected) {
    return;
  }

  dispatch(patchManageDialog({ busy: true, busyAction: 'delete', busyTarget: '', error: '' }));
  dispatch(setTerminalCopyOutput(''));
  dispatch(setTerminalCopyStatus(''));
  dispatch(showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`));

  try {
    const result = (await dispatch(
      environmentApi.endpoints.deleteEnvironment.initiate({ selection, confirmation }),
    ).unwrap()) as DeleteEnvironmentResult;
    const currentSelected = getState().selection.selected;
    const deletedSelected = currentSelected ? selectionKey(currentSelected) === selectionKey(selection) : false;
    if (deletedSelected) {
      dispatch(setSelected(null));
      dispatch(setSessionId(0));
      dispatch(setDebugOutput(''));
      controller.resetTerminal();
    }
    await controller.reloadStateAfterEnvironmentChange();
    dispatch(setManageDialog(defaultManageDialog()));
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    const warnings = [
      result.namespaceDeleteError ? `Namespace deletion failed: ${result.namespaceDeleteError}` : '',
      result.cloudContextStopError ? `Cloud context stop failed: ${result.cloudContextStopError}` : '',
    ].filter(Boolean).join(' ');
    const warning = warnings ? ` ${warnings}` : '';
    dispatch(showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`));
  } catch (error) {
    const message = readError(error);
    dispatch(patchManageDialog({
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    }));
    dispatch(setTerminalCopyOutput(`Failed to delete ${selection.tenant} / ${selection.environment}: ${message}`));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(message));
  }
};

const refreshManageVersionSuggestions = (selectDefault: boolean): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const selection = getState().manageDialog.selection;
    if (!selection) {
      return;
    }
    dispatch(bumpManageDialogVersion());
    const request = getState().requestCounters.manageDialogVersion;
    const raw = await dispatch(
      environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (request !== getState().requestCounters.manageDialogVersion || !getState().manageDialog.open) {
      return;
    }
    dispatch(setVersionSuggestions(suggestions));
    const currentVersion = normalizeDialogValue(getState().manageDialog.version);
    if (selectDefault || (currentVersion && !suggestions.some((suggestion) => suggestion.version === currentVersion))) {
      dispatch(selectManageVersionSuggestion(suggestions[0]));
    }
  };

const startHiddenSession = (
  mode: HiddenSessionMode,
  starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
  const controller = requireController(extra);
  const state = getState();
  const dialog = state.manageDialog;
  const selection = dialog.selection;
  if (dialog.busy || dialog.configLoading || !selection) {
    return;
  }
  const debugOpen = state.layout.debugOpen;
  const runSelection = { ...selection, debug: debugOpen || undefined };
  dispatch(setSelected(selection));
  dispatch(setManageDialog(defaultManageDialog()));
  if (debugOpen) {
    dispatch(setPendingDebugHeader(`$ ${formatDebugCommand(runSelection, mode)}\n`));
  }
  dispatch(setTerminalCopyOutput(''));
  dispatch(setTerminalCopyStatus(''));
  dispatch(showTerminalMessage(hiddenSessionBusyMessage(selection, mode), true));
  controller.fitTerminal();
  const size = controller.terminalSize();
  const result = (await starter(runSelection, size.cols, size.rows)) as StartSessionResult;
  if (result.kind === 'local') {
    await controller.activateLocalAfterCommand(selection, result);
    return;
  }
  dispatch(trackHiddenSession(mode, result.sessionId, runSelection));
  dispatch(registerDebugSession({ sessionId: result.sessionId, selection: runSelection, mode: 'hidden' }));
  dispatch(applyPendingDebugHeader(result.sessionId));
  dispatch(setSessionId(result.sessionId));
  dispatch(syncDebugDisplay());
  controller.resetTerminal();
  controller.focusTerminalSoon();
  controller.queueTerminalResize();
};

const trackHiddenSession = (
  mode: HiddenSessionMode,
  sessionId: number,
  selection: UISelection,
): AppThunk => (dispatch) => {
  if (mode === 'sshd-init') {
    dispatch(trackSSHDInitSession({ sessionId, selection }));
    return;
  }
  dispatch(trackDoctorSession({ sessionId, selection }));
};

const updateManageCloudContextPower = (
  name: string,
  action: (name: string) => Promise<unknown>,
  label: string,
): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const contextName = normalizeDialogValue(name);
  const dialog = getState().manageDialog;
  if (dialog.busy || dialog.configLoading || !dialog.selection || !contextName) {
    return;
  }
  dispatch(patchManageDialog({ busy: true, busyAction: 'cloud-context-power', busyTarget: contextName, error: '' }));
  try {
    const context = (await action(contextName)) as UICloudContextStatus;
    const currentConfig = getState().manageDialog.config;
    dispatch(patchManageDialog({
      config: { ...currentConfig, cloudContext: context },
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    }));
    dispatch(showTerminalMessage(`${label} cloud context ${context.kubernetesContext || context.name}.`));
  } catch (error) {
    const message = readError(error);
    dispatch(patchManageDialog({
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    }));
    dispatch(showTerminalMessage(message));
  }
};

function cloneEnvironmentConfig(config: UIEnvironmentConfig): UIEnvironmentConfig {
  return JSON.parse(JSON.stringify(config));
}

export function manageDialogTabHasUnsavedChanges(tab: ManageTab, config: UIEnvironmentConfig, initial: UIEnvironmentConfig | null): boolean {
  if (!initial) {
    return false;
  }
  const compare = (...keys: Array<keyof UIEnvironmentConfig>): boolean =>
    keys.some((key) => JSON.stringify(config[key]) !== JSON.stringify(initial[key]));
  switch (tab) {
    case 'general':
      return compare('containerRegistry', 'cloudProviderAlias', 'snapshot');
    case 'runtime':
      return compare('runtimePod', 'idle');
    case 'ai':
      return compare('claude');
    case 'ports':
      return false;
    case 'ssh':
      return JSON.stringify(config.sshd?.workspaceSyncEnabled) !== JSON.stringify(initial.sshd?.workspaceSyncEnabled)
        || JSON.stringify(config.sshd?.workspaceSyncLocalPath) !== JSON.stringify(initial.sshd?.workspaceSyncLocalPath);
    case 'delete':
      return false;
  }
}

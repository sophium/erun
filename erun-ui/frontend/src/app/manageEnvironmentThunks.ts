import { StartDoctorSession, StartSSHDInitSession } from '../../wailsjs/go/main/App';
import { cloudApi } from './api/cloudApi';
import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import type { HiddenSessionMode } from './model';
import { runtimePodConfigToDisplay, runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
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

// versionSuggestionRequest tracks the most recent in-flight refresh so a
// stale response cannot clobber the dialog after the user moved on; the
// class version lived as an instance field, so a module-level counter
// is sufficient for the singleton case.
let versionSuggestionRequest = 0;

export const openManageDialog = (selection: UISelection): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.manageDialog = {
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
  };
  void dispatch(refreshManageVersionSuggestions(false));
  void dispatch(loadManageConfig());
};

export const closeManageDialog = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.manageDialog.busy) {
    return;
  }
  controller.state.manageDialog = defaultManageDialog();
  controller.focusTerminalSoon();
};

export const setManageTab = (tab: ManageTab): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.manageDialog.busy) {
    return;
  }
  controller.state.manageDialog = {
    ...controller.state.manageDialog,
    tab,
    choicesOpen: false,
    error: '',
  };
};

export const updateManageDialog = (values: Partial<ManageDialogState>): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.manageDialog.busy) {
    return;
  }
  const versionReset = values.version !== undefined;
  controller.state.manageDialog = {
    ...controller.state.manageDialog,
    ...values,
    error: values.error ?? '',
    ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
  };
};

export const toggleManageVersionChoices = (): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  dispatch(setManageVersionChoicesOpen(!controller.state.manageDialog.choicesOpen));
};

export const setManageVersionChoicesOpen = (open: boolean): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.manageDialog.busy) {
    return;
  }
  controller.state.manageDialog = {
    ...controller.state.manageDialog,
    choicesOpen: open && controller.state.versionSuggestions.length > 0,
  };
};

export const selectManageVersionSuggestion = (suggestion: UIVersionSuggestion | undefined): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.manageDialog.busy) {
      return;
    }
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      version: suggestion?.version || '',
      versionImage: suggestion?.image || '',
      choicesOpen: false,
    };
  };

export const updateManageConfig = (values: Partial<UIEnvironmentConfig>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.manageDialog.busy || controller.state.manageDialog.configLoading) {
      return;
    }
    const config = { ...controller.state.manageDialog.config, ...values };
    if (values.cloudProviderAlias !== undefined) {
      config.cloudContext = undefined;
    }
    controller.state.manageDialog = { ...controller.state.manageDialog, config, error: '' };
  };

export const updateManageClaudeConfig = (values: Partial<UIEnvironmentConfig['claude']>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.manageDialog.busy || controller.state.manageDialog.configLoading) {
      return;
    }
    const merged = { ...controller.state.manageDialog.config.claude, ...values };
    const next: UIEnvironmentConfig['claude'] = {};
    if (merged.useMantle !== undefined) next.useMantle = merged.useMantle;
    if (merged.useBedrock !== undefined) next.useBedrock = merged.useBedrock;
    if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
    if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      config: { ...controller.state.manageDialog.config, claude: next },
      error: '',
    };
  };

export const updateManageSSHDConfig = (values: Partial<UIEnvironmentConfig['sshd']>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.manageDialog.busy || controller.state.manageDialog.configLoading) {
      return;
    }
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      config: {
        ...controller.state.manageDialog.config,
        sshd: { ...controller.state.manageDialog.config.sshd, ...values },
      },
      error: '',
    };
  };

export const chooseWorkspaceSyncLocalFolder = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const dialog = controller.state.manageDialog;
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

export const loadManageConfig = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.manageDialog;
  const selection = dialog.selection;
  if (!dialog.open || !selection) {
    return;
  }
  controller.state.manageDialog = { ...dialog, configLoading: true, error: '' };
  try {
    const result = await dispatch(
      environmentApi.endpoints.getEnvironmentConfig.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const displayConfig = { ...result, runtimePod: runtimePodConfigToDisplay(result.runtimePod) };
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      config: displayConfig,
      initialConfig: cloneEnvironmentConfig(displayConfig),
      configLoading: false,
      resourceStatusLoading: true,
      error: '',
    };
    void dispatch(loadManageResourceStatus(result.kubernetesContext, selection));
  } catch (error) {
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      configLoading: false,
      error: readError(error),
    };
  }
};

const loadManageResourceStatus = (kubernetesContext: string, selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (!controller.state.manageDialog.open) {
      return;
    }
    try {
      const status = await dispatch(
        environmentApi.endpoints.getRuntimeResourceStatus.initiate(
          { kubernetesContext, tenant: selection.tenant, environment: selection.environment },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!controller.state.manageDialog.open) {
        return;
      }
      controller.state.manageDialog = {
        ...controller.state.manageDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
    } catch (error) {
      if (!controller.state.manageDialog.open) {
        return;
      }
      controller.state.manageDialog = {
        ...controller.state.manageDialog,
        resourceStatus: {
          kubernetesContext,
          available: false,
          message: readError(error),
          cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
          memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
        },
        resourceStatusLoading: false,
      };
    }
  };

export const submitManageConfig = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.manageDialog;
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
    controller.state.manageDialog = { ...dialog, error: resourceError };
    return;
  }
  controller.state.manageDialog = { ...dialog, busy: true, busyAction: 'save', busyTarget: '', error: '' };
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
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      config: displayConfig,
      initialConfig: cloneEnvironmentConfig(displayConfig),
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
      pendingRedeploy: true,
    };
  } catch (error) {
    const message = readError(error);
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    controller.showTerminalMessage(message);
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

export const submitManageDeploy = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.manageDialog;
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
    runtimeImage: version ? controller.resolveManageRuntimeImage(version) : '',
  });
};

export const submitManageDelete = (): AppThunk<Promise<void>> => async (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.manageDialog;
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

  controller.state.manageDialog = { ...dialog, busy: true, busyAction: 'delete', busyTarget: '', error: '' };
  controller.state.terminalCopyOutput = '';
  controller.state.terminalCopyStatus = '';
  controller.showTerminalMessage(`Deleting ${selection.tenant} / ${selection.environment}...`);

  try {
    const result = (await dispatch(
      environmentApi.endpoints.deleteEnvironment.initiate({ selection, confirmation }),
    ).unwrap()) as DeleteEnvironmentResult;
    const deletedSelected = controller.state.selected ? selectionKey(controller.state.selected) === selectionKey(selection) : false;
    if (deletedSelected) {
      controller.state.selected = null;
      controller.state.sessionId = 0;
      controller.state.debugOutput = '';
      controller.resetTerminal();
    }
    await controller.reloadStateAfterEnvironmentChange();
    controller.state.manageDialog = defaultManageDialog();
    controller.state.terminalCopyOutput = '';
    controller.state.terminalCopyStatus = '';
    const warnings = [
      result.namespaceDeleteError ? `Namespace deletion failed: ${result.namespaceDeleteError}` : '',
      result.cloudContextStopError ? `Cloud context stop failed: ${result.cloudContextStopError}` : '',
    ].filter(Boolean).join(' ');
    const warning = warnings ? ` ${warnings}` : '';
    controller.showTerminalMessage(`Deleted ${result.tenant} / ${result.environment}.${warning}`);
  } catch (error) {
    const message = readError(error);
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    controller.state.terminalCopyOutput = `Failed to delete ${selection.tenant} / ${selection.environment}: ${message}`;
    controller.state.terminalCopyStatus = '';
    controller.showTerminalMessage(message);
  }
};

const refreshManageVersionSuggestions = (selectDefault: boolean): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = controller.state.manageDialog.selection;
    if (!selection) {
      return;
    }
    const request = ++versionSuggestionRequest;
    const raw = await dispatch(
      environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (request !== versionSuggestionRequest || !controller.state.manageDialog.open) {
      return;
    }
    controller.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(controller.state.manageDialog.version);
    if (selectDefault || (currentVersion && !suggestions.some((suggestion) => suggestion.version === currentVersion))) {
      dispatch(selectManageVersionSuggestion(suggestions[0]));
    }
  };

const startHiddenSession = (
  mode: HiddenSessionMode,
  starter: (selection: UISelection, cols: number, rows: number) => Promise<unknown>,
): AppThunk<Promise<void>> => async (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const dialog = controller.state.manageDialog;
  const selection = dialog.selection;
  if (dialog.busy || dialog.configLoading || !selection) {
    return;
  }
  const runSelection = { ...selection, debug: controller.state.debugOpen || undefined };
  prepareHiddenSession(controller, selection, runSelection, mode);
  controller.fitTerminal();
  const size = controller.terminalSize();
  const result = (await starter(runSelection, size.cols, size.rows)) as StartSessionResult;
  if (result.kind === 'local') {
    await controller.activateLocalAfterCommand(selection, result);
    return;
  }
  trackHiddenSession(controller, mode, result.sessionId, runSelection);
  controller.sessions.registerDebugSession(result.sessionId, runSelection, 'hidden');
  controller.applyPendingDebugHeader(result.sessionId);
  controller.state.sessionId = result.sessionId;
  controller.syncDebugDisplay();
  controller.resetTerminal();
  controller.focusTerminalSoon();
  controller.queueTerminalResize();
};

function prepareHiddenSession(
  controller: NonNullable<ReturnType<typeof requireController>>,
  selection: UISelection,
  runSelection: UISelection,
  mode: HiddenSessionMode,
): void {
  controller.state.selected = selection;
  controller.state.manageDialog = defaultManageDialog();
  if (controller.state.debugOpen) {
    controller.setPendingDebugHeader(`$ ${formatDebugCommand(runSelection, mode)}\n`);
  }
  controller.state.terminalCopyOutput = '';
  controller.state.terminalCopyStatus = '';
  controller.showTerminalMessage(hiddenSessionBusyMessage(selection, mode), true);
}

function trackHiddenSession(
  controller: NonNullable<ReturnType<typeof requireController>>,
  mode: HiddenSessionMode,
  sessionId: number,
  selection: UISelection,
): void {
  if (mode === 'sshd-init') {
    controller.sessions.trackSSHDInitSession(sessionId, selection);
    return;
  }
  controller.sessions.trackDoctorSession(sessionId, selection);
}

const updateManageCloudContextPower = (
  name: string,
  action: (name: string) => Promise<unknown>,
  label: string,
): AppThunk<Promise<void>> => async (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const contextName = normalizeDialogValue(name);
  const dialog = controller.state.manageDialog;
  if (dialog.busy || dialog.configLoading || !dialog.selection || !contextName) {
    return;
  }
  controller.state.manageDialog = { ...dialog, busy: true, busyAction: 'cloud-context-power', busyTarget: contextName, error: '' };
  try {
    const context = (await action(contextName)) as UICloudContextStatus;
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      config: { ...controller.state.manageDialog.config, cloudContext: context },
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: '',
    };
    controller.showTerminalMessage(`${label} cloud context ${context.kubernetesContext || context.name}.`);
  } catch (error) {
    const message = readError(error);
    controller.state.manageDialog = {
      ...controller.state.manageDialog,
      busy: false,
      busyAction: '',
      busyTarget: '',
      error: message,
    };
    controller.showTerminalMessage(message);
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

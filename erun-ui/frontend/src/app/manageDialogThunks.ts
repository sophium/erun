import type { ManageTab, UIEnvironmentConfig, UISelection, UIVersionSuggestion } from '@/types';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import {
  clearManageDeployComponentsRefresh,
  refreshManageDeployComponents,
  scheduleManageDeployComponentsRefresh,
} from './manageDeployComponentsThunks';
import {
  aiSessionLaunchSignature,
  cloneEnvironmentConfig,
  compactClaudeDraft,
  nextPendingRedeploy,
} from './manageDialogHelpers';
import { showTerminalMessage } from './notificationThunks';
import {
  runtimePodConfigToDisplay,
  runtimePodConfigToKubernetes,
  runtimeResourceValidation,
} from './runtimeResources';
import { patchManageDialog, setManageDialog } from './slices/manageDialogSlice';
import { bumpManageDialogVersion } from './slices/requestCountersSlice';
import { setVersionSuggestions } from './slices/tenantsSlice';
import { defaultEnvironmentConfig, defaultManageDialog, type ManageDialogState } from './state';
import { rememberPastContainerRegistry } from './storage';
import type { AppThunk } from './store';
import { relaunchAISessionsForLaunchChange } from './tabRespawnThunks';
import { requireController } from './thunkExtra';
import { normalizeDialogValue, normalizeVersionSuggestions } from './versionSuggestions';

export const openManageDialog =
  (selection: UISelection): AppThunk =>
  (dispatch) => {
    dispatch(
      setManageDialog({
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
        deployComponents: [],
        deployComponentSelection: [],
        deployComponentsLoading: true,
      }),
    );
    void dispatch(refreshManageVersionSuggestions(false));
    void dispatch(loadManageConfig());
    void dispatch(refreshManageDeployComponents());
  };

export const closeManageDialog = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  if (getState().manageDialog.busy) {
    return;
  }
  clearManageDeployComponentsRefresh();
  dispatch(setManageDialog(defaultManageDialog()));
  controller.focusTerminalSoon();
};

export const setManageTab =
  (tab: ManageTab): AppThunk =>
  (dispatch, getState) => {
    if (getState().manageDialog.busy) {
      return;
    }
    dispatch(
      patchManageDialog({
        tab,
        choicesOpen: false,
        error: '',
      }),
    );
  };

export const updateManageDialog =
  (values: Partial<ManageDialogState>): AppThunk =>
  (dispatch, getState) => {
    if (getState().manageDialog.busy) {
      return;
    }
    const versionReset = values.version !== undefined;
    dispatch(
      patchManageDialog({
        ...values,
        error: values.error ?? '',
        ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
      }),
    );
    // A changed deploy version changes which component charts are published, so
    // re-probe the checklist (debounced against per-keystroke free-text edits).
    if (versionReset) {
      dispatch(scheduleManageDeployComponentsRefresh());
    }
  };

export const toggleManageVersionChoices = (): AppThunk => (dispatch, getState) => {
  dispatch(setManageVersionChoicesOpen(!getState().manageDialog.choicesOpen));
};

export const setManageVersionChoicesOpen =
  (open: boolean): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (state.manageDialog.busy) {
      return;
    }
    dispatch(
      patchManageDialog({
        choicesOpen: open && state.tenants.versionSuggestions.length > 0,
      }),
    );
  };

export const selectManageVersionSuggestion =
  (suggestion: UIVersionSuggestion | undefined): AppThunk =>
  (dispatch, getState) => {
    if (getState().manageDialog.busy) {
      return;
    }
    dispatch(
      patchManageDialog({
        version: suggestion?.version ?? '',
        versionImage: suggestion?.image ?? '',
        choicesOpen: false,
      }),
    );
    // Picking a version is discrete, so re-probe the checklist immediately.
    void dispatch(refreshManageDeployComponents());
  };

export const updateManageConfig =
  (values: Partial<UIEnvironmentConfig>): AppThunk =>
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

// The AWS alias is also written to the legacy cloudProviderAlias scalar the
// cloud-context linkage UI still reads, and its resolved cloud context is
// cleared so a stale link cannot outlive the alias change.
export const updateManageCloudAliasSlot =
  (provider: string, alias: string): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    const providerType = provider.trim().toLowerCase();
    const slots = (dialog.config.cloudAliasSlots ?? []).map((slot) =>
      slot.provider.trim().toLowerCase() === providerType ? { ...slot, alias } : slot,
    );
    const config = { ...dialog.config, cloudAliasSlots: slots };
    if (providerType === '' || providerType === 'aws') {
      config.cloudProviderAlias = alias;
      config.cloudContext = undefined;
    }
    dispatch(patchManageDialog({ config, error: '' }));
  };

export const updateManageClaudeConfig =
  (values: Partial<UIEnvironmentConfig['claude']>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchManageDialog({
        config: {
          ...dialog.config,
          claude: compactClaudeDraft({ ...dialog.config.claude, ...values }),
        },
        error: '',
      }),
    );
  };

export const updateManageSSHDConfig =
  (values: Partial<UIEnvironmentConfig['sshd']>): AppThunk =>
  (dispatch, getState) => {
    const dialog = getState().manageDialog;
    if (dialog.busy || dialog.configLoading) {
      return;
    }
    dispatch(
      patchManageDialog({
        config: {
          ...dialog.config,
          sshd: { ...dialog.config.sshd, ...values },
        },
        error: '',
      }),
    );
  };

export const chooseWorkspaceSyncLocalFolder =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const dialog = getState().manageDialog;
    const selection = dialog.selection;
    if (
      dialog.busy ||
      dialog.configLoading ||
      !selection ||
      !dialog.config.sshd.workspaceSyncEnabled
    ) {
      return;
    }
    const folder = await dispatch(
      environmentApi.endpoints.chooseWorkspaceSyncLocalFolder.initiate({
        selection,
        current: dialog.config.sshd.workspaceSyncLocalPath ?? '',
      }),
    ).unwrap();
    const selected = folder.trim();
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
    dispatch(
      patchManageDialog({
        config: displayConfig,
        initialConfig: cloneEnvironmentConfig(displayConfig),
        configLoading: false,
        resourceStatusLoading: true,
        error: '',
      }),
    );
    void dispatch(loadManageResourceStatus(result.kubernetesContext, selection));
  } catch (error) {
    dispatch(
      patchManageDialog({
        configLoading: false,
        error: readError(error),
      }),
    );
  }
};

const loadManageResourceStatus =
  (kubernetesContext: string, selection: UISelection): AppThunk<Promise<void>> =>
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
      dispatch(
        patchManageDialog({
          resourceStatus: status,
          resourceStatusLoading: false,
        }),
      );
    } catch (error) {
      if (!getState().manageDialog.open) {
        return;
      }
      dispatch(
        patchManageDialog({
          resourceStatus: {
            kubernetesContext,
            available: false,
            message: readError(error),
            cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
            memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
          },
          resourceStatusLoading: false,
        }),
      );
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
  const { blockingError } = runtimeResourceValidation(
    dialog.config.runtimePod,
    dialog.resourceStatus,
  );
  if (blockingError) {
    dispatch(patchManageDialog({ error: blockingError }));
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
    for (const entry of result.containerRegistries.length
      ? result.containerRegistries
      : saveConfig.containerRegistries) {
      rememberPastContainerRegistry(entry.registry);
    }
    const displayConfig = { ...result, runtimePod: runtimePodConfigToDisplay(result.runtimePod) };
    const priorConfig = dialog.initialConfig;
    dispatch(
      patchManageDialog({
        config: displayConfig,
        initialConfig: cloneEnvironmentConfig(displayConfig),
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
        // The banner means "the running pod is behind the saved config", so
        // raise it only when this save changed a pod-shaping field (saving
        // autoUpgrade/autoStart must not prompt a pod roll).
        pendingRedeploy: nextPendingRedeploy(dialog.pendingRedeploy, priorConfig, displayConfig),
      }),
    );
    // A changed Claude launch flag only applies when the AI session's
    // create-time program runs, so reopen the env's open AI tabs now rather
    // than leaving a live claude on the stale flags. A save that did not
    // change the launch signature must not churn tabs.
    if (
      priorConfig &&
      aiSessionLaunchSignature(priorConfig) !== aiSessionLaunchSignature(displayConfig)
    ) {
      void dispatch(relaunchAISessionsForLaunchChange(selection));
    }
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
    dispatch(showTerminalMessage(message));
  }
};

const refreshManageVersionSuggestions =
  (selectDefault: boolean): AppThunk<Promise<void>> =>
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
    if (
      request !== getState().requestCounters.manageDialogVersion ||
      !getState().manageDialog.open
    ) {
      return;
    }
    dispatch(setVersionSuggestions(suggestions));
    const currentVersion = normalizeDialogValue(getState().manageDialog.version);
    if (
      selectDefault ||
      (currentVersion && !suggestions.some((suggestion) => suggestion.version === currentVersion))
    ) {
      dispatch(selectManageVersionSuggestion(suggestions[0]));
    }
  };

// The deploy-components thunks (refresh/schedule/toggle/save) live in
// ./manageDeployComponentsThunks and are re-exported through the barrel.

import type { ManageTab, UIEnvironmentConfig, UISelection, UIVersionSuggestion } from '@/types';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { cloneEnvironmentConfig } from './manageDialogHelpers';
import { showTerminalMessage } from './notificationThunks';
import {
  runtimePodConfigToDisplay,
  runtimePodConfigToKubernetes,
  runtimeResourceLimitMessage,
} from './runtimeResources';
import { patchManageDialog, setManageDialog } from './slices/manageDialogSlice';
import { bumpManageDialogVersion } from './slices/requestCountersSlice';
import { setVersionSuggestions } from './slices/tenantsSlice';
import { defaultEnvironmentConfig, defaultManageDialog, type ManageDialogState } from './state';
import { rememberPastContainerRegistry } from './storage';
import type { AppThunk } from './store';
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
      }),
    );
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

export const updateManageClaudeConfig =
  (values: Partial<UIEnvironmentConfig['claude']>): AppThunk =>
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
    if (merged.effort !== undefined) next.effort = merged.effort;
    dispatch(
      patchManageDialog({
        config: { ...dialog.config, claude: next },
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
  const resourceError = runtimeResourceLimitMessage(
    dialog.config.runtimePod,
    dialog.resourceStatus,
  );
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
    dispatch(
      patchManageDialog({
        config: displayConfig,
        initialConfig: cloneEnvironmentConfig(displayConfig),
        busy: false,
        busyAction: '',
        busyTarget: '',
        error: '',
        pendingRedeploy: true,
      }),
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

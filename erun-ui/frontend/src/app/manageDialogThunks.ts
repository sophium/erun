import type {
  ManageTab,
  UIEnvironmentConfig,
  UISelection,
  UIVersionSuggestion,
  UIVersionSuggestions,
} from '@/types';

import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import { loadManageGatewayRouting } from './gatewayRedeployThunks';
import {
  clearManageDeployComponentsRefresh,
  refreshManageDeployComponents,
  scheduleManageDeployComponentsRefresh,
} from './manageDeployComponentsThunks';
import {
  aiSessionLaunchSignature,
  claudeRoutingChanged,
  cloneEnvironmentConfig,
  compactClaudeDraft,
  nextPendingRedeploy,
  versionSourceSignature,
} from './manageDialogHelpers';
import { refreshManageExposures } from './manageExposureThunks';
import { showTerminalError } from './notificationThunks';
import {
  runtimePodConfigToDisplay,
  runtimePodConfigToKubernetes,
  runtimeResourceValidation,
  unavailableRuntimeResourceStatus,
} from './runtimeResources';
import { patchManageDialog, setManageDialog } from './slices/manageDialogSlice';
import { bumpManageDialogVersion } from './slices/requestCountersSlice';
import { defaultEnvironmentConfig, defaultManageDialog, type ManageDialogState } from './state';
import { rememberPastContainerRegistry } from './storage';
import type { AppThunk } from './store';
import { relaunchAISessionsForLaunchChange } from './tabRespawnThunks';
import { requireController } from './thunkExtra';
import {
  normalizeVersionSuggestionNotices,
  normalizeVersionSuggestions,
} from './versionSuggestions';

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
        versionSuggestions: [],
        versionSuggestionNotices: [],
        deployComponents: [],
        deployComponentSelection: [],
        deployComponentsLoading: true,
        runtimeChartPlan: null,
        health: null,
        healthLoading: false,
        exposures: { configured: false, restricted: false, services: [] },
        exposuresLoading: true,
        exposeForm: { service: '', backendService: '', targetIP: '', port: '' },
        exposeBusy: false,
        exposeError: '',
        environmentServices: { configured: false, restricted: false, services: [] },
        unexposeConfirming: false,
        unexposeBusy: false,
        unexposeError: '',
      }),
    );
    void dispatch(refreshManageVersionSuggestions(false));
    void dispatch(loadManageConfig());
    void dispatch(refreshManageDeployComponents());
    void dispatch(refreshManageExposures());
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
        // Loading now so a reopened picker never shows the previous version's charts.
        ...(versionReset
          ? { versionImage: '', choicesOpen: false, deployComponentsLoading: true }
          : {}),
      }),
    );
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
    dispatch(patchManageDialog({ choicesOpen: open }));
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
        choicesOpen: true,
      }),
    );
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

// Also writes the legacy cloudProviderAlias and clears the resolved cloud context
// so a stale link cannot outlive the alias.
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
  dispatch(
    patchManageDialog({ configLoading: true, error: '', health: null, healthLoading: false }),
  );
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
    void dispatch(loadManageGatewayRouting(selection));
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
          resourceStatus: unavailableRuntimeResourceStatus(kubernetesContext, readError(error)),
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
        pendingRedeploy: nextPendingRedeploy(dialog.pendingRedeploy, priorConfig, displayConfig),
      }),
    );
    dispatch(applyManageSaveConsequences(selection, priorConfig, displayConfig));
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

// applyManageSaveConsequences refreshes what a save invalidates, each gated on the
// values it depends on.
const applyManageSaveConsequences =
  (
    selection: UISelection,
    priorConfig: UIEnvironmentConfig | null,
    savedConfig: UIEnvironmentConfig,
  ): AppThunk =>
  (dispatch) => {
    // Without a prior config, re-query: a redundant listing beats a stale one.
    if (
      !priorConfig ||
      versionSourceSignature(priorConfig) !== versionSourceSignature(savedConfig)
    ) {
      void dispatch(refreshManageVersionSuggestions(false));
    }
    if ((priorConfig?.runtimeChart ?? '') !== (savedConfig.runtimeChart ?? '')) {
      void dispatch(refreshManageDeployComponents());
    }
    // Launch flags apply only on session restart; a routing change waits for redeploy.
    if (
      priorConfig &&
      aiSessionLaunchSignature(priorConfig) !== aiSessionLaunchSignature(savedConfig) &&
      !claudeRoutingChanged(priorConfig, savedConfig)
    ) {
      void dispatch(relaunchAISessionsForLaunchChange(selection));
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
    const stale = (): boolean =>
      request !== getState().requestCounters.manageDialogVersion || !getState().manageDialog.open;
    let raw: UIVersionSuggestions;
    try {
      raw = await dispatch(
        environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
      ).unwrap();
    } catch (error) {
      // Otherwise the empty list reads as "no versions".
      if (!stale()) {
        dispatch(patchManageDialog({ error: readError(error) }));
      }
      return;
    }
    const suggestions = normalizeVersionSuggestions(raw.suggestions);
    const notices = normalizeVersionSuggestionNotices(raw.notices ?? []);
    if (stale()) {
      return;
    }
    dispatch(
      patchManageDialog({ versionSuggestions: suggestions, versionSuggestionNotices: notices }),
    );
    // Never override a version the operator picked or typed while this was in flight.
    const defaultSuggestion = suggestions[0];
    if (selectDefault && defaultSuggestion) {
      dispatch(selectManageVersionSuggestion(defaultSuggestion));
    }
  };

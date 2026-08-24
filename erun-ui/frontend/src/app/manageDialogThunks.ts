import type {
  ManageTab,
  UIEnvironmentConfig,
  UISelection,
  UIVersionSuggestion,
  UIVersionSuggestions,
} from '@/types';

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
  versionSourceSignature,
} from './manageDialogHelpers';
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
        // Mark the checklist loading now (not just when the debounced probe
        // fires) so reopening the picker mid-debounce shows the loading state
        // for the new version, never the previous version's charts.
        ...(versionReset
          ? { versionImage: '', choicesOpen: false, deployComponentsLoading: true }
          : {}),
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
    // The popover is one panel — versions and that version's components — so it
    // opens on request even before any version suggestion has loaded.
    dispatch(patchManageDialog({ choicesOpen: open }));
  };

export const selectManageVersionSuggestion =
  (suggestion: UIVersionSuggestion | undefined): AppThunk =>
  (dispatch, getState) => {
    if (getState().manageDialog.busy) {
      return;
    }
    // Keep the popover open after a pick so the operator continues to the chosen
    // version's component checklist in the same panel.
    dispatch(
      patchManageDialog({
        version: suggestion?.version ?? '',
        versionImage: suggestion?.image ?? '',
        choicesOpen: true,
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
  // A config reload makes any prior health result stale, so clear it; the
  // operator re-runs "Check environment" against the freshly loaded config.
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
        // The banner means "the running pod is behind the saved config", so
        // raise it only when this save changed a pod-shaping field (saving
        // autoUpgrade/autoStart must not prompt a pod roll).
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

// applyManageSaveConsequences re-runs what a save invalidates beyond the config
// it persisted — the version picker, the chart probe, and the AI tabs are all
// computed from the pre-save values, and nothing else re-reads them while the
// dialog stays open. Each is gated on the values it actually depends on, so an
// unrelated save costs no registry round-trip, no re-probe, and no tab churn.
const applyManageSaveConsequences =
  (
    selection: UISelection,
    priorConfig: UIEnvironmentConfig | null,
    savedConfig: UIEnvironmentConfig,
  ): AppThunk =>
  (dispatch) => {
    // The picker lists versions from the registries the environment resolves to,
    // and is otherwise loaded only when the dialog opens. A save that changed
    // those registries must re-query, or the one the operator just added is
    // absent from the list AND from the notices — indistinguishable from a
    // registry erun cannot use, in the one place its effect is read. A missing
    // prior config cannot be diffed, so re-query: a redundant listing is cheaper
    // than a silently stale one.
    if (
      !priorConfig ||
      versionSourceSignature(priorConfig) !== versionSourceSignature(savedConfig)
    ) {
      void dispatch(refreshManageVersionSuggestions(false));
    }
    // Stating (or clearing) the runtime chart changes which chart a deploy would
    // install, so the probe behind the panel is re-run rather than left
    // describing the chart the environment used to ride.
    if ((priorConfig?.runtimeChart ?? '') !== (savedConfig.runtimeChart ?? '')) {
      void dispatch(refreshManageDeployComponents());
    }
    // A changed Claude launch flag only applies when the AI session's
    // create-time program runs, so reopen the env's open AI tabs now rather
    // than leaving a live claude on the stale flags. A save that did not
    // change the launch signature must not churn tabs.
    if (
      priorConfig &&
      aiSessionLaunchSignature(priorConfig) !== aiSessionLaunchSignature(savedConfig)
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
      // A listing the transport could not complete leaves the picker empty, so
      // say why here rather than letting the rejection escape unhandled and the
      // empty list read as "this environment has no versions".
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
    // Only auto-select a default when explicitly asked. Never override a version
    // the operator picked or typed: the dialog opens at '', so any non-empty
    // version when this async fetch resolves is a fresh operator action, and an
    // in-flight suggestions load landing a beat later must not clobber it — doing
    // so reset the version and collapsed the "Components to deploy" panel back to
    // its gated hint with no error shown.
    const defaultSuggestion = suggestions[0];
    if (selectDefault && defaultSuggestion) {
      dispatch(selectManageVersionSuggestion(defaultSuggestion));
    }
  };

// The deploy-components thunks (refresh/schedule/toggle/save) live in
// ./manageDeployComponentsThunks and are re-exported through the barrel.

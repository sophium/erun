import type { UISelection, UIVersionSuggestion } from '@/types';

import { environmentApi } from './api/environmentApi';
import { refreshKubernetesContexts } from './dialogContextsThunks';
import {
  normalizedEnvironmentDialogValues,
  rememberEnvironmentDialogSelection,
  validEnvironmentDialogValues,
} from './environmentDialogState';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
import { startDeploySelection, startInitSelection } from './sessionThunks';
import { patchEnvironmentDialog, setEnvironmentDialog } from './slices/environmentDialogSlice';
import {
  bumpEnvironmentDialogResourceStatus,
  bumpEnvironmentDialogVersion,
} from './slices/requestCountersSlice';
import { setSelected } from './slices/selectionSlice';
import { setVersionSuggestions } from './slices/tenantsSlice';
import { defaultEnvironmentDialog, type EnvironmentDialogState } from './state';
import { loadSavedPastContainerRegistries } from './storage';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { normalizeDialogValue, normalizeVersionSuggestions } from './versionSuggestions';

// environmentDialogThunks own the open/edit/submit lifecycle for the
// "create or deploy environment" modal. The version-suggestion debounce
// handle stays module-local — it is a setTimeout cancellation token, not
// state. The request counters used to ignore stale responses now live in
// the requestCounters slice.

let versionSuggestionTimer = 0;

export const openInitializeDialog = (): AppThunk => (dispatch, getState) => {
  const state = getState();
  const tenantDefault = state.selection.selected?.tenant ?? state.tenants.tenants[0]?.name ?? '';
  const containerRegistryDefault = loadSavedPastContainerRegistries()[0] ?? '';
  dispatch(
    setEnvironmentDialog({
      open: true,
      actionMode: 'init',
      tenant: tenantDefault,
      environment: '',
      version: state.tenants.versionSuggestions[0]?.version ?? '',
      kubernetesContext: '',
      kubernetesContexts: [],
      kubernetesContextsLoading: true,
      resourceStatus: null,
      resourceStatusLoading: false,
      runtimePod: defaultEnvironmentDialog().runtimePod,
      containerRegistry: containerRegistryDefault,
      envType: 'remote-agent',
      localRepoPath: '',
      noGit: false,
      setDefaultTenant: true,
      versionImage: state.tenants.versionSuggestions[0]?.image ?? '',
      choicesOpen: false,
      busy: false,
      error: '',
    }),
  );
  void dispatch(refreshKubernetesContexts());
  void dispatch(refreshDialogVersionSuggestions(true));
};

export const closeEnvironmentDialog = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  if (getState().environmentDialog.busy) {
    return;
  }
  dispatch(setEnvironmentDialog(defaultEnvironmentDialog()));
  controller.focusTerminalSoon();
};

export const updateEnvironmentDialog =
  (values: Partial<EnvironmentDialogState>): AppThunk =>
  (dispatch, getState) => {
    if (getState().environmentDialog.busy) {
      return;
    }
    const versionReset = values.version !== undefined;
    dispatch(
      patchEnvironmentDialog({
        ...values,
        error: values.error ?? '',
        ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
      }),
    );
    if (values.tenant !== undefined) {
      scheduleDialogVersionSuggestionRefresh(true, dispatch);
    }
    if (values.kubernetesContext !== undefined) {
      void dispatch(refreshEnvironmentRuntimeResources(values.kubernetesContext));
    }
  };

export const toggleEnvironmentVersionChoices = (): AppThunk => (dispatch, getState) => {
  dispatch(setEnvironmentVersionChoicesOpen(!getState().environmentDialog.choicesOpen));
};

export const setEnvironmentVersionChoicesOpen =
  (open: boolean): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (state.environmentDialog.busy) {
      return;
    }
    dispatch(
      patchEnvironmentDialog({
        choicesOpen: open && state.tenants.versionSuggestions.length > 0,
      }),
    );
  };

export const selectEnvironmentVersionSuggestion =
  (suggestion: UIVersionSuggestion | undefined): AppThunk =>
  (dispatch, getState) => {
    if (getState().environmentDialog.busy) {
      return;
    }
    dispatch(
      patchEnvironmentDialog({
        version: suggestion?.version ?? '',
        versionImage: suggestion?.image ?? '',
        choicesOpen: false,
      }),
    );
  };

export const submitEnvironmentDialog =
  (form: HTMLFormElement): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const dialog = state.environmentDialog;
    if (dialog.busy) {
      return;
    }
    const tenantExists = state.tenants.tenants.some(
      (tenant) => tenant.name === dialog.tenant.trim(),
    );
    const selection = environmentDialogSelection(
      dialog,
      state.tenants.versionSuggestions,
      tenantExists,
    );
    if (!selection) {
      dispatch(patchEnvironmentDialog({ error: '' }));
      form.reportValidity();
      return;
    }
    const resourceError =
      dialog.actionMode === 'init'
        ? runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus)
        : '';
    if (resourceError) {
      dispatch(patchEnvironmentDialog({ error: resourceError }));
      return;
    }

    rememberEnvironmentDialogSelection(selection, dialog.actionMode);
    beginEnvironmentDialogSubmit(dispatch, dialog, selection);
    const previousSelected = state.selection.selected;
    try {
      if (dialog.actionMode === 'deploy') {
        await dispatch(startDeploySelection(selection));
      } else {
        await dispatch(startInitSelection(selection));
      }
      dispatch(setEnvironmentDialog(defaultEnvironmentDialog()));
      controller.focusTerminalSoon();
    } catch (error) {
      const message = readError(error);
      dispatch(setSelected(previousSelected));
      dispatch(
        patchEnvironmentDialog({
          busy: false,
          error: message,
        }),
      );
      dispatch(showTerminalMessage(message));
    }
  };

function environmentDialogSelection(
  dialog: EnvironmentDialogState,
  versionSuggestions: UIVersionSuggestion[],
  tenantExists: boolean,
): UISelection | null {
  const values = normalizedEnvironmentDialogValues(dialog);
  if (!validEnvironmentDialogValues(values, dialog.actionMode)) {
    return null;
  }
  const isInit = dialog.actionMode === 'init';
  const initFields = isInit ? environmentDialogInitFields(dialog, values, tenantExists) : {};
  // noGit only changes behavior on the remote-worktree init path
  // (ensureRemoteRepository in erun-common/init.go). For local-agent
  // init there is no remote repo, so suppress NoGit regardless of any
  // stale dialog state from a previous type selection.
  const noGit = isInit && dialog.envType === 'local-agent' ? false : dialog.noGit;
  return {
    tenant: values.tenant,
    environment: values.environment,
    version: values.version,
    runtimeImage: resolveEnvironmentRuntimeImage(values.version, dialog, versionSuggestions),
    noGit,
    ...initFields,
  };
}

function environmentDialogInitFields(
  dialog: EnvironmentDialogState,
  values: ReturnType<typeof normalizedEnvironmentDialogValues>,
  tenantExists: boolean,
): Partial<UISelection> {
  const runtimePod = runtimePodConfigToKubernetes(dialog.runtimePod);
  const isLocalAgent = dialog.envType === 'local-agent';
  return {
    runtimeCpu: runtimePod.cpu,
    runtimeMemory: runtimePod.memory,
    kubernetesContext: values.kubernetesContext,
    containerRegistry: values.containerRegistry,
    type: dialog.envType,
    localRepoPath: isLocalAgent ? values.localRepoPath : undefined,
    // Bootstrap is derived from env type and tenant state, not a user
    // choice. local-agent never passes --bootstrap because the local
    // code path (EnsureDefaultDevopsModuleWithVersion in
    // erun-common/init.go) creates the devops module unconditionally
    // and idempotently. For remote-agent / runtime we pass --bootstrap
    // only when the tenant is new — no envs yet in state — so the
    // first init scaffolds the remote devops module; for an existing
    // tenant the module is already there and re-running the bootstrap
    // step would be wasted SSH work.
    bootstrap: !isLocalAgent && !tenantExists,
    setDefaultTenant: dialog.setDefaultTenant,
  };
}

function resolveEnvironmentRuntimeImage(
  version: string,
  dialog: EnvironmentDialogState,
  versionSuggestions: UIVersionSuggestion[],
): string {
  if (dialog.versionImage) {
    return dialog.versionImage;
  }
  const suggestion = versionSuggestions.find((value) => value.version === version);
  return suggestion?.image ?? '';
}

function beginEnvironmentDialogSubmit(
  dispatch: (action: ReturnType<typeof patchEnvironmentDialog>) => unknown,
  dialog: EnvironmentDialogState,
  selection: UISelection,
): void {
  dispatch(
    patchEnvironmentDialog({
      tenant: selection.tenant,
      environment: selection.environment,
      version: selection.version ?? '',
      kubernetesContext: selection.kubernetesContext ?? '',
      runtimePod: dialog.runtimePod,
      containerRegistry: selection.containerRegistry ?? '',
      busy: true,
      error: '',
      choicesOpen: false,
    }),
  );
}

function scheduleDialogVersionSuggestionRefresh(
  selectDefault: boolean,
  dispatch: (thunk: AppThunk<Promise<void>>) => Promise<void>,
): void {
  if (versionSuggestionTimer) {
    window.clearTimeout(versionSuggestionTimer);
  }
  versionSuggestionTimer = window.setTimeout(() => {
    void dispatch(refreshDialogVersionSuggestions(selectDefault));
  }, 250);
}

export const refreshDialogVersionSuggestions =
  (selectDefault: boolean): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    dispatch(bumpEnvironmentDialogVersion());
    const request = getState().requestCounters.environmentDialogVersion;
    const dialog = getState().environmentDialog;
    const selection = {
      tenant: normalizeDialogValue(dialog.tenant),
      environment: normalizeDialogValue(dialog.environment),
      action: dialog.actionMode,
    };
    const raw = await dispatch(
      environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (
      request !== getState().requestCounters.environmentDialogVersion ||
      !getState().environmentDialog.open
    ) {
      return;
    }
    dispatch(setVersionSuggestions(suggestions));
    const currentVersion = normalizeDialogValue(getState().environmentDialog.version);
    if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      dispatch(selectEnvironmentVersionSuggestion(suggestions[0]));
    }
  };

const refreshEnvironmentRuntimeResources =
  (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    dispatch(bumpEnvironmentDialogResourceStatus());
    const request = getState().requestCounters.environmentDialogResourceStatus;
    const context = normalizeDialogValue(kubernetesContext);
    const dialog = getState().environmentDialog;
    if (!dialog.open || dialog.actionMode !== 'init' || !context) {
      return;
    }
    dispatch(
      patchEnvironmentDialog({
        resourceStatusLoading: true,
        resourceStatus: null,
      }),
    );
    try {
      const current = getState().environmentDialog;
      const status = await dispatch(
        environmentApi.endpoints.getRuntimeResourceStatus.initiate(
          {
            kubernetesContext: context,
            tenant: normalizeDialogValue(current.tenant),
            environment: normalizeDialogValue(current.environment),
          },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (
        request !== getState().requestCounters.environmentDialogResourceStatus ||
        !getState().environmentDialog.open
      ) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          resourceStatus: status,
          resourceStatusLoading: false,
        }),
      );
    } catch (error) {
      if (
        request !== getState().requestCounters.environmentDialogResourceStatus ||
        !getState().environmentDialog.open
      ) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          resourceStatus: {
            kubernetesContext: context,
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

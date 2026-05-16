import { environmentApi } from './api/environmentApi';
import { readError } from './errors';
import {
  normalizedEnvironmentDialogValues,
  rememberEnvironmentDialogSelection,
  validEnvironmentDialogValues,
} from './environmentDialogState';
import { showTerminalMessage } from './notificationThunks';
import { runtimePodConfigToKubernetes, runtimeResourceLimitMessage } from './runtimeResources';
import {
  defaultEnvironmentDialog,
  type EnvironmentDialogState,
} from './state';
import { loadSavedPastContainerRegistries } from './storage';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { normalizeDialogValue, normalizeVersionSuggestions } from './versionSuggestions';
import type { UISelection, UIVersionSuggestion } from '@/types';

// environmentDialogThunks own the open/edit/submit lifecycle for the
// "create or deploy environment" modal. Version-suggestion polling and
// kubernetes-context refresh state live here as module-level counters
// because the controller no longer owns them.

let versionSuggestionTimer = 0;
let versionSuggestionRequest = 0;
let environmentResourceStatusRequest = 0;

export const openInitializeDialog = (): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const tenantDefault = controller.state.selected?.tenant || controller.state.tenants[0]?.name || '';
  const containerRegistryDefault = loadSavedPastContainerRegistries()[0] || '';
  controller.state.environmentDialog = {
    open: true,
    actionMode: 'init',
    tenant: tenantDefault,
    environment: '',
    version: controller.state.versionSuggestions[0]?.version || '',
    kubernetesContext: '',
    kubernetesContexts: [],
    kubernetesContextsLoading: true,
    resourceStatus: null,
    resourceStatusLoading: false,
    runtimePod: defaultEnvironmentDialog().runtimePod,
    containerRegistry: containerRegistryDefault,
    noGit: false,
    bootstrap: false,
    setDefaultTenant: true,
    versionImage: controller.state.versionSuggestions[0]?.image || '',
    choicesOpen: false,
    busy: false,
    error: '',
  };
  void controller.refreshKubernetesContexts();
  void dispatch(refreshDialogVersionSuggestions(true));
};

export const closeEnvironmentDialog = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.environmentDialog.busy) {
    return;
  }
  controller.state.environmentDialog = defaultEnvironmentDialog();
  controller.focusTerminalSoon();
};

export const updateEnvironmentDialog = (
  values: Partial<EnvironmentDialogState>,
): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.environmentDialog.busy) {
    return;
  }
  const versionReset = values.version !== undefined;
  controller.state.environmentDialog = {
    ...controller.state.environmentDialog,
    ...values,
    error: values.error ?? '',
    ...(versionReset ? { versionImage: '', choicesOpen: false } : {}),
  };
  if (values.tenant !== undefined) {
    scheduleDialogVersionSuggestionRefresh(true, dispatch);
  }
  if (values.kubernetesContext !== undefined) {
    void dispatch(refreshEnvironmentRuntimeResources(values.kubernetesContext));
  }
};

export const toggleEnvironmentVersionChoices = (): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  dispatch(setEnvironmentVersionChoicesOpen(!controller.state.environmentDialog.choicesOpen));
};

export const setEnvironmentVersionChoicesOpen = (open: boolean): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.environmentDialog.busy) {
      return;
    }
    controller.state.environmentDialog = {
      ...controller.state.environmentDialog,
      choicesOpen: open && controller.state.versionSuggestions.length > 0,
    };
  };

export const selectEnvironmentVersionSuggestion = (
  suggestion: UIVersionSuggestion | undefined,
): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (controller.state.environmentDialog.busy) {
    return;
  }
  controller.state.environmentDialog = {
    ...controller.state.environmentDialog,
    version: suggestion?.version || '',
    versionImage: suggestion?.image || '',
    choicesOpen: false,
  };
};

export const submitEnvironmentDialog = (form: HTMLFormElement): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const dialog = controller.state.environmentDialog;
    if (dialog.busy) {
      return;
    }
    const selection = environmentDialogSelection(dialog, controller.state.versionSuggestions);
    if (!selection) {
      controller.state.environmentDialog = { ...dialog, error: '' };
      form.reportValidity();
      return;
    }
    const resourceError = dialog.actionMode === 'init' ? runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus) : '';
    if (resourceError) {
      controller.state.environmentDialog = { ...dialog, error: resourceError };
      return;
    }

    rememberEnvironmentDialogSelection(selection, dialog.actionMode);
    beginEnvironmentDialogSubmit(controller, dialog, selection);
    const previousSelected = controller.state.selected;
    try {
      if (dialog.actionMode === 'deploy') {
        await controller.startDeploySelection(selection);
      } else {
        await controller.startInitSelection(selection);
      }
      controller.state.environmentDialog = defaultEnvironmentDialog();
      controller.focusTerminalSoon();
    } catch (error) {
      const message = readError(error);
      controller.state.selected = previousSelected;
      controller.state.environmentDialog = {
        ...controller.state.environmentDialog,
        busy: false,
        error: message,
      };
      dispatch(showTerminalMessage(message));
    }
  };

function environmentDialogSelection(
  dialog: EnvironmentDialogState,
  versionSuggestions: UIVersionSuggestion[],
): UISelection | null {
  const values = normalizedEnvironmentDialogValues(dialog);
  if (!validEnvironmentDialogValues(values, dialog.actionMode)) {
    return null;
  }
  const isInit = dialog.actionMode === 'init';
  const runtimePod = runtimePodConfigToKubernetes(dialog.runtimePod);
  return {
    tenant: values.tenant,
    environment: values.environment,
    version: values.version,
    runtimeImage: resolveEnvironmentRuntimeImage(values.version, dialog, versionSuggestions),
    runtimeCpu: isInit ? runtimePod.cpu : undefined,
    runtimeMemory: isInit ? runtimePod.memory : undefined,
    kubernetesContext: isInit ? values.kubernetesContext : undefined,
    containerRegistry: isInit ? values.containerRegistry : undefined,
    noGit: dialog.noGit,
    bootstrap: isInit ? dialog.bootstrap : undefined,
    setDefaultTenant: isInit ? dialog.setDefaultTenant : undefined,
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
  return suggestion?.image || '';
}

function beginEnvironmentDialogSubmit(
  controller: NonNullable<ReturnType<typeof requireController>>,
  dialog: EnvironmentDialogState,
  selection: UISelection,
): void {
  controller.state.environmentDialog = {
    ...dialog,
    tenant: selection.tenant,
    environment: selection.environment,
    version: selection.version || '',
    kubernetesContext: selection.kubernetesContext || '',
    runtimePod: dialog.runtimePod,
    containerRegistry: selection.containerRegistry || '',
    busy: true,
    error: '',
    choicesOpen: false,
  };
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

export const refreshDialogVersionSuggestions = (selectDefault: boolean): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const request = ++versionSuggestionRequest;
    const dialog = controller.state.environmentDialog;
    const selection = {
      tenant: normalizeDialogValue(dialog.tenant),
      environment: normalizeDialogValue(dialog.environment),
      action: dialog.actionMode,
    };
    const raw = await dispatch(
      environmentApi.endpoints.getVersionSuggestions.initiate(selection, { forceRefetch: true }),
    ).unwrap();
    const suggestions = normalizeVersionSuggestions(raw);
    if (request !== versionSuggestionRequest || !controller.state.environmentDialog.open) {
      return;
    }
    controller.state.versionSuggestions = suggestions;
    const currentVersion = normalizeDialogValue(controller.state.environmentDialog.version);
    if (selectDefault || !suggestions.some((suggestion) => suggestion.version === currentVersion)) {
      dispatch(selectEnvironmentVersionSuggestion(suggestions[0]));
    }
  };

const refreshEnvironmentRuntimeResources = (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const request = ++environmentResourceStatusRequest;
    const context = normalizeDialogValue(kubernetesContext);
    if (
      !controller.state.environmentDialog.open ||
      controller.state.environmentDialog.actionMode !== 'init' ||
      !context
    ) {
      return;
    }
    controller.state.environmentDialog = {
      ...controller.state.environmentDialog,
      resourceStatusLoading: true,
      resourceStatus: null,
    };
    try {
      const status = await dispatch(
        environmentApi.endpoints.getRuntimeResourceStatus.initiate(
          {
            kubernetesContext: context,
            tenant: normalizeDialogValue(controller.state.environmentDialog.tenant),
            environment: normalizeDialogValue(controller.state.environmentDialog.environment),
          },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (request !== environmentResourceStatusRequest || !controller.state.environmentDialog.open) {
        return;
      }
      controller.state.environmentDialog = {
        ...controller.state.environmentDialog,
        resourceStatus: status,
        resourceStatusLoading: false,
      };
    } catch (error) {
      if (request !== environmentResourceStatusRequest || !controller.state.environmentDialog.open) {
        return;
      }
      controller.state.environmentDialog = {
        ...controller.state.environmentDialog,
        resourceStatus: {
          kubernetesContext: context,
          available: false,
          message: readError(error),
          cpu: { total: 0, used: 0, free: 0, unit: 'cores', formatted: '' },
          memory: { total: 0, used: 0, free: 0, unit: 'GiB', formatted: '' },
        },
        resourceStatusLoading: false,
      };
    }
  };

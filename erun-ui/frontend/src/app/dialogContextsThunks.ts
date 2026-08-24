import { environmentApi } from './api/environmentApi';
import { kubernetesApi } from './api/kubernetesApi';
import { readError } from './errors';
import { unavailableRuntimeResourceStatus } from './runtimeResources';
import { selectDialogKubernetesContext } from './selectors';
import { patchEnvironmentDialog } from './slices/environmentDialogSlice';
import type { AppThunk } from './store';
import { normalizeDialogValue } from './versionSuggestions';

const refreshDialogRuntimeResources =
  (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const context = normalizeDialogValue(kubernetesContext);
    let dialog = getState().environmentDialog;
    if (!dialog.open) {
      return;
    }
    // No selected context → clear stale capacity rather than leaving a figure from
    // a previously resolved context showing under an empty selection.
    if (!context) {
      dispatch(patchEnvironmentDialog({ resourceStatus: null, resourceStatusLoading: false }));
      return;
    }
    dispatch(
      patchEnvironmentDialog({
        resourceStatusLoading: true,
        resourceStatus: null,
      }),
    );
    try {
      dialog = getState().environmentDialog;
      const status = await dispatch(
        environmentApi.endpoints.getRuntimeResourceStatus.initiate(
          {
            kubernetesContext: context,
            tenant: normalizeDialogValue(dialog.tenant),
            environment: normalizeDialogValue(dialog.environment),
          },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!getState().environmentDialog.open) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          resourceStatus: status,
          resourceStatusLoading: false,
        }),
      );
    } catch (error) {
      if (!getState().environmentDialog.open) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          resourceStatus: unavailableRuntimeResourceStatus(context, readError(error)),
          resourceStatusLoading: false,
        }),
      );
    }
  };

// refreshDialogClusterRegistry detects the in-cluster erun-registry for the
// selected context and defaults the dialog to using it. Cleared when no context
// is selected or none is deployed, so the container-registry choice always
// matches the selected cluster.
export const refreshDialogClusterRegistry =
  (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const context = normalizeDialogValue(kubernetesContext);
    if (!getState().environmentDialog.open) {
      return;
    }
    if (!context) {
      dispatch(patchEnvironmentDialog({ clusterRegistry: null, useClusterRegistry: false }));
      return;
    }
    try {
      const status = await dispatch(
        environmentApi.endpoints.getClusterRegistry.initiate(
          { kubernetesContext: context },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!getState().environmentDialog.open) {
        return;
      }
      const deployed = status.deployed;
      dispatch(
        patchEnvironmentDialog({
          clusterRegistry: deployed ? status : null,
          // Default to the in-cluster registry whenever one is detected for the
          // chosen context; the user can still opt out to a static registry.
          useClusterRegistry: deployed,
        }),
      );
    } catch (error) {
      if (!getState().environmentDialog.open) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          clusterRegistry: null,
          useClusterRegistry: false,
          error: readError(error),
        }),
      );
    }
  };

// forceRefetch is required: without it RTK Query pins the previous
// (possibly transient-empty) context list to the dialog, so Rescan can
// never recover after a kubeconfig change.
export const refreshKubernetesContexts =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    try {
      const result = await dispatch(
        kubernetesApi.endpoints.getKubernetesContexts.initiate(undefined, {
          forceRefetch: true,
        }),
      ).unwrap();
      const contexts = result.map((context) => context.trim()).filter(Boolean);
      const dialog = getState().environmentDialog;
      if (!dialog.open) {
        return;
      }
      const resolved = selectDialogKubernetesContext(getState(), contexts);
      dispatch(
        patchEnvironmentDialog({
          kubernetesContexts: contexts,
          kubernetesContext: resolved,
          kubernetesContextsLoading: false,
        }),
      );
      await dispatch(refreshDialogRuntimeResources(resolved));
      await dispatch(refreshDialogClusterRegistry(resolved));
    } catch (error) {
      const dialog = getState().environmentDialog;
      if (!dialog.open) {
        return;
      }
      dispatch(
        patchEnvironmentDialog({
          kubernetesContexts: [],
          kubernetesContext: '',
          kubernetesContextsLoading: false,
          clusterRegistry: null,
          useClusterRegistry: false,
          error: readError(error),
        }),
      );
    }
  };

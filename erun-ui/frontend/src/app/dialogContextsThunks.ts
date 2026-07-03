import { environmentApi } from './api/environmentApi';
import { kubernetesApi } from './api/kubernetesApi';
import { readError } from './errors';
import { selectDialogKubernetesContext } from './selectors';
import { patchEnvironmentDialog } from './slices/environmentDialogSlice';
import type { AppThunk } from './store';
import { normalizeDialogValue } from './versionSuggestions';

const refreshDialogRuntimeResources =
  (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const context = normalizeDialogValue(kubernetesContext);
    let dialog = getState().environmentDialog;
    if (!dialog.open || !context) {
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
          error: readError(error),
        }),
      );
    }
  };

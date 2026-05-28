import { environmentApi } from './api/environmentApi';
import { kubernetesApi } from './api/kubernetesApi';
import { readError } from './errors';
import { selectDialogKubernetesContext } from './selectors';
import { patchEnvironmentDialog } from './slices/environmentDialogSlice';
import type { AppThunk } from './store';
import { normalizeDialogValue } from './versionSuggestions';

// dialogContextsThunks own the env-init dialog's "available kubernetes
// contexts" + "selected context runtime resources" flows. The user-driven
// refresh (kubernetesContext field changes) lives in
// environmentDialogThunks; this module covers dialog-open seeding and the
// user-triggered "rescan k8s contexts" button.

// refreshDialogRuntimeResources hits the runtime-resources endpoint for
// the dialog's currently-selected k8s context and patches the dialog with
// the resulting CPU/memory totals. Internal helper; not exported because
// callers should drive it via refreshKubernetesContexts.
const refreshDialogRuntimeResources =
  (kubernetesContext: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const context = normalizeDialogValue(kubernetesContext);
    let dialog = getState().environmentDialog;
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

// refreshKubernetesContexts re-scans kubeconfig for available contexts and
// patches the dialog. Triggered by the "rescan k8s contexts" button and
// after cloud-context power changes that may have rewritten kubeconfig.
// forceRefetch is required: without it RTK Query returns the cached
// result of the previous LoadKubernetesContexts call, so a transient
// empty result (or simply a need to re-scan after a kubeconfig edit) gets
// pinned to the dialog and Rescan can never recover.
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
      if (!dialog.open || dialog.actionMode !== 'init') {
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
      if (!dialog.open || dialog.actionMode !== 'init') {
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

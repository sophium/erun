import { stateApi } from './api/stateApi';
import { selectLoadedKubernetesContexts } from './dialogContextsThunks';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { openSelection } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import { setCloudProviders, setTenants, setVersionSuggestions } from './slices/tenantsSlice';
import type { AppThunk } from './store';
import { normalizeVersionSuggestions } from './versionSuggestions';

// boot loads initial app state from the backend, hydrates the tenants /
// cloud-providers / version-suggestions / k8s-contexts slices, and either
// re-opens the previously-selected env or surfaces a "choose an env"
// hint. Controller mount dispatches it once per app instance.
export const boot = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  try {
    dispatch(showTerminalMessage('Loading environments...', true));
    const loaded = await dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    dispatch(setTenants(loaded.tenants || []));
    dispatch(setCloudProviders(loaded.cloudProviders || []));
    dispatch(setSelected(loaded.selected || null));
    dispatch(setVersionSuggestions(normalizeVersionSuggestions(loaded.versionSuggestions || [])));
    await dispatch(selectLoadedKubernetesContexts(loaded.kubernetesContexts || []));
    if (loaded.message) {
      dispatch(showTerminalMessage(loaded.message));
      return;
    }

    const selected = getState().selection.selected;
    if (selected) {
      await dispatch(openSelection(selected));
      return;
    }

    dispatch(showTerminalMessage('Choose an environment from the left pane.'));
  } catch (error: unknown) {
    dispatch(showTerminalMessage(readError(error)));
  }
};

// reloadStateAfterEnvironmentChange refetches initial state after backend
// signals an env was created/removed (config watcher, environment-init
// completion). Preserves the user's existing tenants / cloudProviders /
// versionSuggestions if the new payload omits them, since this is a delta
// refresh — boot() is the authoritative initial load.
export const reloadStateAfterEnvironmentChange =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    try {
      const loaded = await dispatch(
        stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
      ).unwrap();
      const current = getState().tenants;
      dispatch(setTenants(loaded.tenants || []));
      dispatch(setCloudProviders(loaded.cloudProviders || current.cloudProviders));
      dispatch(
        setVersionSuggestions(
          normalizeVersionSuggestions(loaded.versionSuggestions || current.versionSuggestions),
        ),
      );
      await dispatch(selectLoadedKubernetesContexts(loaded.kubernetesContexts || []));
    } catch {
      // Silent failure: env-change reloads are best-effort.
    }
  };

import { stateApi } from './api/stateApi';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { openSelection } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import { setCloudProviders, setTenants, setVersionSuggestions } from './slices/tenantsSlice';
import type { AppThunk } from './store';
import { normalizeVersionSuggestions } from './versionSuggestions';

// boot loads initial app state from the backend, hydrates the tenants /
// cloud-providers / version-suggestions slices, and either re-opens the
// previously-selected env or surfaces a "choose an env" hint. Controller
// mount dispatches it once per app instance. The env-init dialog manages
// its own kubectl context list (openInitializeDialog →
// refreshKubernetesContexts); boot does not seed it.
export const boot = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  try {
    dispatch(showTerminalMessage('Loading environments...', true));
    const loaded = await dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    dispatch(setTenants(loaded.tenants));
    dispatch(setCloudProviders(loaded.cloudProviders ?? []));
    dispatch(setSelected(loaded.selected ?? null));
    dispatch(setVersionSuggestions(normalizeVersionSuggestions(loaded.versionSuggestions ?? [])));
    if (loaded.message !== undefined && loaded.message !== '') {
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
// completion). Preserves the user's existing cloudProviders /
// versionSuggestions if the new payload omits them, since this is a delta
// refresh — boot() is the authoritative initial load. Does NOT touch the
// env-init dialog's kubernetes-context list: a stale environments-changed
// tick used to wipe a populated dropdown because uiState never carried
// contexts to seed from.
export const reloadStateAfterEnvironmentChange =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    // initiate() registers a cache subscription that must be released, or
    // repeated reloads (e.g. handleEnvironmentInitialized's retry loop) leak
    // subscriptions and eventually stall RTK Query so a later refetch never
    // resolves. Hold the request handle and unsubscribe in finally.
    const request = dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    );
    try {
      const loaded = await request.unwrap();
      const current = getState().tenants;
      dispatch(setTenants(loaded.tenants));
      dispatch(setCloudProviders(loaded.cloudProviders ?? current.cloudProviders));
      dispatch(
        setVersionSuggestions(
          normalizeVersionSuggestions(loaded.versionSuggestions ?? current.versionSuggestions),
        ),
      );
    } catch (error) {
      // Env-change reloads are best-effort, but swallowing the failure
      // silently used to leave the sidebar stale after a successful
      // `erun init` with no diagnostic at all. Log it so a failed refresh is
      // visible in the dev console / ErrorBoundary diagnostics; callers that
      // depend on the new env surfacing (handleEnvironmentInitialized) retry
      // rather than trusting a single best-effort pass.
      console.error('reloadStateAfterEnvironmentChange failed:', error);
    } finally {
      request.unsubscribe();
    }
  };

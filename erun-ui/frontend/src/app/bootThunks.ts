import { stateApi } from './api/stateApi';
import { readError } from './errors';
import { showTerminalMessage } from './notificationThunks';
import { openSelection } from './sessionThunks';
import { setSelected } from './slices/selectionSlice';
import {
  setCloudProviders,
  setTenants,
  setVersionSuggestionNotices,
  setVersionSuggestions,
} from './slices/tenantsSlice';
import type { AppThunk } from './store';
import {
  normalizeVersionSuggestionNotices,
  normalizeVersionSuggestions,
} from './versionSuggestions';

// boot deliberately does not seed the env-init dialog's kubectl context
// list — that dialog owns and refreshes its own.
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
    dispatch(
      setVersionSuggestionNotices(
        normalizeVersionSuggestionNotices(loaded.versionSuggestionNotices ?? []),
      ),
    );
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

// This is a delta refresh of the sidebar/tenants, not the authoritative initial
// load (boot() is), so it preserves existing cloudProviders when the new payload
// omits them. It deliberately does NOT touch version suggestions: those are
// resolved per-env by whichever dialog is open (each owns/refreshes its own), and
// getInitialState always recomputes them for the sidebar-selected env — so writing
// them here clobbered an open picker with the wrong env's versions on every
// environment-change tick (e.g. leaving only the upstream fallback while a tenant
// build fired deltas). It must also not touch the env-init dialog's
// kubernetes-context list: a stale environments-changed tick used to wipe a
// populated dropdown.
export const reloadStateAfterEnvironmentChange =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    // initiate() registers a cache subscription that must be released, or
    // repeated reloads (e.g. handleEnvironmentInitialized's retry loop) leak
    // subscriptions and eventually stall RTK Query so a later refetch never
    // resolves.
    const request = dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    );
    try {
      const loaded = await request.unwrap();
      const current = getState().tenants;
      dispatch(setTenants(loaded.tenants));
      dispatch(setCloudProviders(loaded.cloudProviders ?? current.cloudProviders));
    } catch (error) {
      // Env-change reloads are best-effort, but swallowing the failure
      // silently used to leave the sidebar stale after a successful
      // `erun init`. Log it so the failure is visible; callers that depend on
      // the new env surfacing (handleEnvironmentInitialized) retry rather than
      // trusting a single best-effort pass.
      console.error('reloadStateAfterEnvironmentChange failed:', error);
    } finally {
      request.unsubscribe();
    }
  };

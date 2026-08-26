import type { UITenant } from '@/types';

import { stateApi } from './api/stateApi';
import { planEnvActivitySeed } from './envActivitySeed';
import { readError } from './errors';
import { showTerminalError, showTerminalMessage } from './notificationThunks';
import { loadOrchestrators, restoreOpenOrchestrators } from './orchestratorThunks';
import { openSelection } from './sessionThunks';
import { setEnvActivityForEnv } from './slices/envStatusSlice';
import { setSelected } from './slices/selectionSlice';
import {
  setCloudProviders,
  setTenants,
  setVersionSuggestionNotices,
  setVersionSuggestions,
} from './slices/tenantsSlice';
import type { AppDispatch, AppThunk } from './store';
import {
  normalizeVersionSuggestionNotices,
  normalizeVersionSuggestions,
} from './versionSuggestions';
import { loadInterruptedActivityNotice } from './windowCloseThunks';

// Seeds each env's busy/reachable row from the poller's own last observation
// (erun#1216) — the same snapshot-seeding shape loadOrchestrators uses for
// the orchestrator rows, needed here for the same reason: the env-activity
// Wails event only fires on a transition, so a boot with no prior transition
// to replay (a page reload, not a process restart) would otherwise render a
// still-busy env as idle until its next one.
function seedEnvActivity(dispatch: AppDispatch, tenants: UITenant[]): void {
  for (const seed of planEnvActivitySeed(tenants)) {
    dispatch(setEnvActivityForEnv(seed));
  }
}

// The generated UIState type says tenants is never null, but that is a
// promise about the Go struct, not about what actually crosses the wire — a
// stub, or a future backend regression, can still send JSON null. Array.isArray
// is a real runtime check here, not a redundant null-coalesce against an
// already-narrowed type, so boot() cannot crash on this before the titlebar
// even has a chance to say why.
function normalizeBootTenants(tenants: UITenant[]): UITenant[] {
  return Array.isArray(tenants) ? tenants : [];
}

// "Choose from the left pane" contradicts the sidebar's own "No environments
// yet" empty state when there is nothing yet to choose — name the actual
// first action instead.
function noSelectionBootMessage(tenants: UITenant[]): string {
  const hasAnyEnvironment = tenants.some((tenant) => tenant.environments.length > 0);
  return hasAnyEnvironment
    ? 'Choose an environment from the left pane.'
    : 'Create your first environment from the left pane.';
}

// boot deliberately does not seed the env-init dialog's kubectl context
// list — that dialog owns and refreshes its own.
export const boot = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  try {
    dispatch(showTerminalMessage('Loading environments...', true));
    const loaded = await dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    const tenants = normalizeBootTenants(loaded.tenants);
    dispatch(setTenants(tenants));
    seedEnvActivity(dispatch, tenants);
    dispatch(setCloudProviders(loaded.cloudProviders ?? []));
    dispatch(setSelected(loaded.selected ?? null));
    dispatch(setVersionSuggestions(normalizeVersionSuggestions(loaded.versionSuggestions ?? [])));
    dispatch(
      setVersionSuggestionNotices(
        normalizeVersionSuggestionNotices(loaded.versionSuggestionNotices ?? []),
      ),
    );
    // A previous launch's confirmed "close anyway" may have interrupted a
    // build/deploy/release with no record until now; report it once, right
    // after the state it interrupted has loaded (erun#1214).
    await dispatch(loadInterruptedActivityNotice());
    if (loaded.message !== undefined && loaded.message !== '') {
      dispatch(showTerminalMessage(loaded.message));
      return;
    }

    // Every orchestrator that was open when the desktop last ran — plus the one
    // a rebuild+restart handed off, if any — is where the operator left off, so
    // honor all of it before — and instead of — the default environment
    // selection. One of them ends up owning the pane; the rest come back idle.
    await dispatch(loadOrchestrators());
    if (await dispatch(restoreOpenOrchestrators())) {
      return;
    }

    const selected = getState().selection.selected;
    if (selected) {
      await dispatch(openSelection(selected));
      return;
    }

    dispatch(showTerminalMessage(noSelectionBootMessage(getState().tenants.tenants)));
  } catch (error: unknown) {
    dispatch(showTerminalError(readError(error)));
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

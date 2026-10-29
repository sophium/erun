import type { UIState, UITenant } from '@/types';

import { stateApi } from './api/stateApi';
import { planEnvActivitySeed } from './envActivitySeed';
import { planEnvNodeSeed } from './envNodeSeed';
import { planEnvUsageSeed } from './envUsageSeed';
import { readError } from './errors';
import { showTerminalError, showTerminalMessage } from './notificationThunks';
import { loadOrchestrators, restoreOpenOrchestrators } from './orchestratorThunks';
import { openSelection } from './sessionThunks';
import { setEnvActivityForEnv, setEnvNodeForEnv, setEnvUsageForEnv } from './slices/envStatusSlice';
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

// The env-activity event fires only on transitions, so a reload seeds rows
// from the poller's last observation.
function seedEnvActivity(dispatch: AppDispatch, tenants: UITenant[]): void {
  for (const seed of planEnvActivitySeed(tenants)) {
    dispatch(setEnvActivityForEnv(seed));
  }
}

function seedEnvUsage(dispatch: AppDispatch, tenants: UITenant[]): void {
  for (const seed of planEnvUsageSeed(tenants)) {
    dispatch(setEnvUsageForEnv(seed));
  }
}

function seedEnvNodes(dispatch: AppDispatch, tenants: UITenant[]): void {
  for (const seed of planEnvNodeSeed(tenants)) {
    dispatch(setEnvNodeForEnv(seed));
  }
}

// The generated type says non-null, but the wire can still carry null.
function normalizeBootTenants(tenants: UITenant[]): UITenant[] {
  return Array.isArray(tenants) ? tenants : [];
}

// Avoid "choose from the left pane" when there is nothing to choose.
function noSelectionBootMessage(tenants: UITenant[]): string {
  const hasAnyEnvironment = tenants.some((tenant) => tenant.environments.length > 0);
  return hasAnyEnvironment
    ? 'Choose an environment from the left pane.'
    : 'Create your first environment from the left pane.';
}

// reportBootLoadOutcome reports an unreadable config as an error (not as an
// empty state) or an informational message; true means boot should stop.
function reportBootLoadOutcome(dispatch: AppDispatch, loaded: UIState): boolean {
  if (loaded.configUnreadable) {
    dispatch(showTerminalError(loaded.message ?? 'Some configuration could not be read.'));
    return true;
  }
  if (loaded.message !== undefined && loaded.message !== '') {
    dispatch(showTerminalMessage(loaded.message));
    return true;
  }
  return false;
}

// boot does not seed the env-init dialog's context list; that dialog owns it.
export const boot = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  try {
    dispatch(showTerminalMessage('Loading environments...', true));
    const loaded = await dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    ).unwrap();
    const tenants = normalizeBootTenants(loaded.tenants);
    dispatch(setTenants(tenants));
    seedEnvActivity(dispatch, tenants);
    seedEnvUsage(dispatch, tenants);
    seedEnvNodes(dispatch, tenants);
    dispatch(setCloudProviders(loaded.cloudProviders ?? []));
    // Something (e.g. an orchestrator) may own the terminal by the time state
    // loads; restoring the last selection would clobber it. Orchestrators are not
    // loaded yet, so check sessionId rather than selectActiveSessionOrchestrator.
    if (getState().terminal.sessionId === 0) {
      dispatch(setSelected(loaded.selected ?? null));
    }
    dispatch(setVersionSuggestions(normalizeVersionSuggestions(loaded.versionSuggestions ?? [])));
    dispatch(
      setVersionSuggestionNotices(
        normalizeVersionSuggestionNotices(loaded.versionSuggestionNotices ?? []),
      ),
    );
    await dispatch(loadInterruptedActivityNotice());
    if (reportBootLoadOutcome(dispatch, loaded)) {
      return;
    }

    // Restored orchestrators take precedence over the default selection.
    await dispatch(loadOrchestrators());
    if (await dispatch(restoreOpenOrchestrators())) {
      return;
    }

    const selected = getState().selection.selected;
    if (selected) {
      await dispatch(openSelection(selected, { isDefaultLandingOpen: true }));
      return;
    }

    dispatch(showTerminalMessage(noSelectionBootMessage(getState().tenants.tenants)));
  } catch (error: unknown) {
    dispatch(showTerminalError(readError(error)));
  }
};

// A delta refresh: keeps cloudProviders when omitted, and must not touch
// version suggestions or the env-init context list, which open dialogs own.
export const reloadStateAfterEnvironmentChange =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    // RTK Query ignores forceRefetch while the same query is pending, so wait for
    // any in-flight request first.
    await dispatch(stateApi.util.getRunningQueryThunk('getInitialState', undefined));
    // Release the subscription initiate() registers, or repeated reloads stall RTK Query.
    const request = dispatch(
      stateApi.endpoints.getInitialState.initiate(undefined, { forceRefetch: true }),
    );
    try {
      const loaded = await request.unwrap();
      // A degraded read's empty tenant list must not replace the current one.
      if (loaded.configUnreadable) {
        dispatch(showTerminalError(loaded.message ?? 'Some configuration could not be read.'));
        return;
      }
      const current = getState().tenants;
      dispatch(setTenants(loaded.tenants));
      dispatch(setCloudProviders(loaded.cloudProviders ?? current.cloudProviders));
    } catch (error) {
      // Callers that need the new env (handleEnvironmentInitialized) retry.
      console.error('reloadStateAfterEnvironmentChange failed:', error);
    } finally {
      request.unsubscribe();
    }
  };

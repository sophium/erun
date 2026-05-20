import type { UIIdleStatus, UISelection } from '@/types';

import { idleApi } from './api/idleApi';
import { restoreEnvTabsAfterContextRunning } from './envRestoreThunks';
import { setIdleStatus } from './slices/idleSlice';
import { bumpIdleStatus } from './slices/requestCountersSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

// cloudContextStatusOf reads the lowercased cloud-context status from an
// idle-status payload; "" means no managed cloud or no idle status.
function cloudContextStatusOf(status: UIIdleStatus | null): string {
  return (status?.cloudContextStatus ?? '').trim().toLowerCase();
}

// reactToCloudContextTransition fires the env-restore thunk when the
// poll observes a non-running → running flip for the selected env.
// tryReconnect refuses respawn while the context is stopped and drops
// the AI/ERun tabs (see erun-ui/terminal_sessions.go:973); without an
// explicit re-spawn on this transition, the env returns to Running but
// its tabs stay gone. The titlebar Play button's success path already
// re-opens; this catches the recovery-after-transient-error path where
// the start command errored but the instance landed in Running anyway.
function reactToCloudContextTransition(
  previousStatus: string,
  nextStatus: string,
  selection: UISelection,
): AppThunk {
  return (dispatch) => {
    if (nextStatus !== 'running' || previousStatus === '' || previousStatus === 'running') {
      return;
    }
    void dispatch(restoreEnvTabsAfterContextRunning(selection));
  };
}

function isRequestStillCurrent(
  request: number,
  getState: Parameters<AppThunk>[1],
  selection?: { tenant: string; environment: string },
): boolean {
  if (request !== getState().requestCounters.idleStatus) {
    return false;
  }
  if (!selection) {
    return true;
  }
  const current = getState().selection.selected;
  return current?.tenant === selection.tenant && current.environment === selection.environment;
}

// refreshIdleStatus polls the backend for the currently-selected env's
// idle status and re-arms the polling timer on the controller. The
// recursive schedule keeps polling alive across selection changes (a
// fresh request bumps the counter; in-flight stale requests detect they
// no longer match and skip their state write).
//
// The poll-timer handle stays on the controller because it is a
// setTimeout cancellation token tied to the mount lifecycle; thunks
// reach it through `extra.controller`.
export const refreshIdleStatus =
  (): AppThunk<Promise<void>> => async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    dispatch(bumpIdleStatus());
    const request = getState().requestCounters.idleStatus;
    const selection = getState().selection.selected;
    if (!selection) {
      if (getState().idle.idleStatus) {
        dispatch(setIdleStatus(null));
      }
      controller.scheduleIdleStatusPoll();
      return;
    }

    try {
      const status = await dispatch(
        idleApi.endpoints.getIdleStatus.initiate(selection, { forceRefetch: true }),
      ).unwrap();
      if (isRequestStillCurrent(request, getState, selection)) {
        const previousStatus = cloudContextStatusOf(getState().idle.idleStatus);
        const nextStatus = cloudContextStatusOf(status);
        dispatch(setIdleStatus(status));
        dispatch(reactToCloudContextTransition(previousStatus, nextStatus, selection));
      }
    } catch {
      if (isRequestStillCurrent(request, getState) && getState().idle.idleStatus) {
        dispatch(setIdleStatus(null));
      }
    } finally {
      if (isRequestStillCurrent(request, getState)) {
        controller.scheduleIdleStatusPoll();
      }
    }
  };

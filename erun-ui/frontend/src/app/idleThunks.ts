import type { UIIdleStatus, UISelection } from '@/types';

import { CancelPendingIdleStop } from '../../wailsjs/go/main/App';
import { idleApi } from './api/idleApi';
import { restoreEnvTabsAfterContextRunning } from './envRestoreThunks';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { setIdleStatus } from './slices/idleSlice';
import { bumpIdleStatus } from './slices/requestCountersSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

function cloudContextStatusOf(status: UIIdleStatus | null): string {
  return (status?.cloudContextStatus ?? '').trim().toLowerCase();
}

// A context returning to Running does not restore its tabs on its own:
// tryReconnect drops the AI/ERun tabs while it is stopped and won't respawn
// them. The Play button re-opens them on its own success path; this handles
// the recovery case where the start command errored but the instance reached
// Running anyway.
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

// refreshIdleStatus polls the selected env's idle status and re-arms the
// self-rescheduling poll timer. A request whose selection changed mid-flight
// detects it is stale and skips its state write.
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

// cancelPendingIdleStop dismisses the grace-period auto-stop warning for the
// env. This is a one-shot snooze, not a permanent opt-out: if the user stays
// idle, the next poll re-arms the warning with a fresh grace window.
export const cancelPendingIdleStop =
  (selection: UISelection): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await CancelPendingIdleStop(selection);
      void dispatch(refreshIdleStatus());
    } catch (error) {
      dispatch(
        showNotification('error', `Failed to cancel pending auto-stop: ${readError(error)}`),
      );
    }
  };

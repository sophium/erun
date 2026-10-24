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

// tryReconnect drops the AI/ERun tabs while stopped; reopen them when a context
// reaches Running even though its start command errored.
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

// refreshIdleStatus skips its state write when the selection changed mid-flight.
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
      // Several triggers share this selection, and RTK Query's condition() bails a
      // forced refetch while one is in flight, so wait for it first.
      await dispatch(idleApi.util.getRunningQueryThunk('getIdleStatus', selection));
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

// cancelPendingIdleStop is a one-shot snooze; the next idle poll re-arms it.
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

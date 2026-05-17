import { idleApi } from './api/idleApi';
import { setIdleStatus } from './slices/idleSlice';
import { bumpIdleStatus } from './slices/requestCountersSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

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
      const current = getState().selection.selected;
      if (
        request === getState().requestCounters.idleStatus &&
        current?.tenant === selection.tenant &&
        current.environment === selection.environment
      ) {
        dispatch(setIdleStatus(status));
      }
    } catch {
      if (request === getState().requestCounters.idleStatus && getState().idle.idleStatus) {
        dispatch(setIdleStatus(null));
      }
    } finally {
      if (request === getState().requestCounters.idleStatus) {
        controller.scheduleIdleStatusPoll();
      }
    }
  };

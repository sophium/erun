import { createSlice } from '@reduxjs/toolkit';

// Per-flow monotonic counters for ignoring stale async responses: a thunk bumps
// its counter before its async work and commits the result only if it is still current.

export interface RequestCountersState {
  reviewDiff: number;
  environmentDialogVersion: number;
  environmentDialogResourceStatus: number;
  manageDialogVersion: number;
  idleStatus: number;
}

const initialState: RequestCountersState = {
  reviewDiff: 0,
  environmentDialogVersion: 0,
  environmentDialogResourceStatus: 0,
  manageDialogVersion: 0,
  idleStatus: 0,
};

export const requestCountersSlice = createSlice({
  name: 'requestCounters',
  initialState,
  reducers: {
    bumpReviewDiff(state) {
      state.reviewDiff += 1;
    },
    bumpEnvironmentDialogVersion(state) {
      state.environmentDialogVersion += 1;
    },
    bumpEnvironmentDialogResourceStatus(state) {
      state.environmentDialogResourceStatus += 1;
    },
    bumpManageDialogVersion(state) {
      state.manageDialogVersion += 1;
    },
    bumpIdleStatus(state) {
      state.idleStatus += 1;
    },
  },
});

export const {
  bumpReviewDiff,
  bumpEnvironmentDialogVersion,
  bumpEnvironmentDialogResourceStatus,
  bumpManageDialogVersion,
  bumpIdleStatus,
} = requestCountersSlice.actions;
export default requestCountersSlice.reducer;

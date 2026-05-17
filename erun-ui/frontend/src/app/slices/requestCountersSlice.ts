import { createSlice } from '@reduxjs/toolkit';

// requestCounters is a monotonic counter per async flow that needs
// "ignore stale responses". Thunks dispatch the matching bump action, read
// the new value, perform their async work, then compare to the current
// counter when committing. The counters are pure machinery — they do not
// render — but living in Redux means a single observable source of truth.

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

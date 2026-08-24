import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { ActivityQueueEntry } from '../activityQueueState';

// Backs the "close anyway?" confirmation the desktop shows when the operator
// tries to close the window while a build/deploy/release is still running
// (erun#1214): closing used to SIGKILL every in-flight job with no warning.
export interface CloseGateState {
  open: boolean;
  running: ActivityQueueEntry[];
  confirming: boolean;
  error: string;
}

const initialState: CloseGateState = {
  open: false,
  running: [],
  confirming: false,
  error: '',
};

export const closeGateSlice = createSlice({
  name: 'closeGate',
  initialState,
  reducers: {
    openCloseGate(state, action: PayloadAction<ActivityQueueEntry[]>) {
      state.open = true;
      state.running = action.payload;
      state.confirming = false;
      state.error = '';
    },
    dismissCloseGate() {
      return initialState;
    },
    setCloseGateConfirming(state, action: PayloadAction<boolean>) {
      state.confirming = action.payload;
    },
    setCloseGateError(state, action: PayloadAction<string>) {
      state.error = action.payload;
      state.confirming = false;
    },
  },
});

export const { openCloseGate, dismissCloseGate, setCloseGateConfirming, setCloseGateError } =
  closeGateSlice.actions;
export default closeGateSlice.reducer;

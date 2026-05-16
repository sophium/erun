import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIIdleStatus } from '@/types';

export interface IdleState {
  idleStatus: UIIdleStatus | null;
  idleCloudContextBusy: boolean;
}

const initialState: IdleState = {
  idleStatus: null,
  idleCloudContextBusy: false,
};

export const idleSlice = createSlice({
  name: 'idle',
  initialState,
  reducers: {
    setIdleStatus(state, action: PayloadAction<UIIdleStatus | null>) {
      state.idleStatus = action.payload;
    },
    setIdleCloudContextBusy(state, action: PayloadAction<boolean>) {
      state.idleCloudContextBusy = action.payload;
    },
    setAll(_state, action: PayloadAction<IdleState>) {
      return action.payload;
    },
  },
});

export const {
  setIdleStatus,
  setIdleCloudContextBusy,
  setAll: setIdleAll,
} = idleSlice.actions;
export default idleSlice.reducer;

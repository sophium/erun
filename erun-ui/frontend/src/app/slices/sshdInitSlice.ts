import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { SSHDInitOutcome } from '../state';

export interface SSHDInitState {
  lastSSHDInitBySelection: Record<string, SSHDInitOutcome>;
}

const initialState: SSHDInitState = {
  lastSSHDInitBySelection: {},
};

export const sshdInitSlice = createSlice({
  name: 'sshdInit',
  initialState,
  reducers: {
    recordSSHDInitOutcome(state, action: PayloadAction<{ key: string; outcome: SSHDInitOutcome }>) {
      state.lastSSHDInitBySelection[action.payload.key] = action.payload.outcome;
    },
    setAll(_state, action: PayloadAction<SSHDInitState>) {
      return action.payload;
    },
  },
});

export const { recordSSHDInitOutcome, setAll: setSSHDInitAll } = sshdInitSlice.actions;
export default sshdInitSlice.reducer;

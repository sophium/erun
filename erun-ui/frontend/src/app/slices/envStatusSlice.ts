import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// envStatusSlice keeps the per-env real-condition flag driven by the Go-side
// env-status Wails event (issue #470). The sidebar's open dot reads it so a
// row with live tabs does not claim "running" (green) while the env is
// actually stopped or its deploy failed — tab presence alone is not
// running-ness. Keyed by `${tenant}\x00${environment}` (the same
// selectionKey() used by tabsByEnv etc.) and stored sparsely: an absent key
// is the healthy state.
export type EnvRealStatus = 'stopped' | 'failed';

export interface EnvStatusState {
  statusByEnv: Record<string, EnvRealStatus>;
}

const initialState: EnvStatusState = {
  statusByEnv: {},
};

export const envStatusSlice = createSlice({
  name: 'envStatus',
  initialState,
  reducers: {
    setEnvStatusForEnv(state, action: PayloadAction<{ key: string; status: string }>) {
      const { key, status } = action.payload;
      if (status === 'stopped' || status === 'failed') {
        state.statusByEnv[key] = status;
      } else {
        Reflect.deleteProperty(state.statusByEnv, key);
      }
    },
  },
});

export const { setEnvStatusForEnv } = envStatusSlice.actions;
export default envStatusSlice.reducer;

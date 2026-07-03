import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env real status behind the sidebar's open dot: a row with live tabs must
// not read as "running" (green) when the env is actually stopped or its deploy
// failed — tab presence alone is not running-ness. An absent key means healthy.
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

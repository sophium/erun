import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env real status behind the sidebar's open dot: a row with live tabs must
// not read as "running" (green) when the env is actually stopped or its deploy
// failed — tab presence alone is not running-ness. An absent key means healthy.
//
// The two stopped kinds are distinct because their recovery is: 'stopped' is a
// stopped cloud context (started from the titlebar), 'runtime-stopped' is a
// runtime scaled to zero (woken by opening the environment). Both are stopped,
// neither is a failure.
export type EnvRealStatus = 'stopped' | 'runtime-stopped' | 'failed';

const envRealStatuses: readonly string[] = ['stopped', 'runtime-stopped', 'failed'];

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
      if (envRealStatuses.includes(status)) {
        state.statusByEnv[key] = status as EnvRealStatus;
      } else {
        Reflect.deleteProperty(state.statusByEnv, key);
      }
    },
  },
});

export const { setEnvStatusForEnv } = envStatusSlice.actions;
export default envStatusSlice.reducer;

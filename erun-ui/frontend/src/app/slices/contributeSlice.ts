import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

export type DiffSource = 'env' | 'erun';

export interface ContributeState {
  flagsByEnv: Record<string, boolean>;
  diffSourceByEnv: Record<string, DiffSource>;
}

const initialState: ContributeState = {
  flagsByEnv: {},
  diffSourceByEnv: {},
};

export const contributeSlice = createSlice({
  name: 'contribute',
  initialState,
  reducers: {
    setContributeFlag(state, action: PayloadAction<{ key: string; enabled: boolean }>) {
      const { key, enabled } = action.payload;
      if (enabled) {
        state.flagsByEnv[key] = true;
        state.diffSourceByEnv[key] ??= 'erun';
      } else {
        Reflect.deleteProperty(state.flagsByEnv, key);
        Reflect.deleteProperty(state.diffSourceByEnv, key);
      }
    },
    setDiffSource(state, action: PayloadAction<{ key: string; source: DiffSource }>) {
      state.diffSourceByEnv[action.payload.key] = action.payload.source;
    },
    setAllFlags(state, action: PayloadAction<Record<string, boolean>>) {
      state.flagsByEnv = { ...action.payload };
    },
  },
});

export const { setContributeFlag, setDiffSource, setAllFlags } = contributeSlice.actions;
export default contributeSlice.reducer;

export function contributeEnvKey(tenant: string, environment: string): string {
  return `${tenant}/${environment}`;
}

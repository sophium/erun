import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIUpgradePlanItem } from '@/types';

// Drives the "Upgrade all" preview dialog; it does not run `erun upgrade`.
export interface UpgradeAllState {
  open: boolean;
  loading: boolean;
  error: string;
  items: UIUpgradePlanItem[];
  choices: Record<string, string>;
}

const initialState: UpgradeAllState = {
  open: false,
  loading: false,
  error: '',
  items: [],
  choices: {},
};

export const upgradeAllSlice = createSlice({
  name: 'upgradeAll',
  initialState,
  reducers: {
    openUpgradeAllDialog(state) {
      state.open = true;
      state.loading = true;
      state.error = '';
      state.items = [];
      state.choices = {};
    },
    setUpgradeAllPlan(state, action: PayloadAction<UIUpgradePlanItem[]>) {
      state.loading = false;
      state.error = '';
      state.items = action.payload;
      state.choices = {};
    },
    setUpgradeAllError(state, action: PayloadAction<string>) {
      state.loading = false;
      state.error = action.payload;
    },
    setUpgradeAllChoice(state, action: PayloadAction<{ key: string; version: string }>) {
      state.choices[action.payload.key] = action.payload.version;
    },
    closeUpgradeAllDialog() {
      return initialState;
    },
  },
});

export const {
  openUpgradeAllDialog,
  setUpgradeAllPlan,
  setUpgradeAllError,
  setUpgradeAllChoice,
  closeUpgradeAllDialog,
} = upgradeAllSlice.actions;
export default upgradeAllSlice.reducer;

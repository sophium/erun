import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIUpgradePlanItem } from '@/types';

// upgradeAll holds the cross-env "Upgrade all" preview dialog state: the
// resolved plan (every opted-in env with its channel and current → target),
// the per-env version the operator picked when an env's registries offered
// several newer versions (issue #527), plus loading/error while
// ResolveUpgradePlan runs. Confirming runs the actual `erun upgrade`; this
// slice only drives the preview.
export interface UpgradeAllState {
  open: boolean;
  loading: boolean;
  error: string;
  items: UIUpgradePlanItem[];
  // choices maps "<tenant>/<environment>" to the version the operator picked
  // for an env with more than one newer candidate. Cleared whenever the dialog
  // re-opens or closes.
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

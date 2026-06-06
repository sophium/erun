import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIUpgradePlanItem } from '@/types';

// upgradeAll holds the cross-env "Upgrade all" preview dialog state: the
// resolved plan (every opted-in env with its channel and current → target),
// plus loading/error while ResolveUpgradePlan runs. Confirming runs the actual
// `erun upgrade`; this slice only drives the preview.
export interface UpgradeAllState {
  open: boolean;
  loading: boolean;
  error: string;
  items: UIUpgradePlanItem[];
}

const initialState: UpgradeAllState = {
  open: false,
  loading: false,
  error: '',
  items: [],
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
    },
    setUpgradeAllPlan(state, action: PayloadAction<UIUpgradePlanItem[]>) {
      state.loading = false;
      state.error = '';
      state.items = action.payload;
    },
    setUpgradeAllError(state, action: PayloadAction<string>) {
      state.loading = false;
      state.error = action.payload;
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
  closeUpgradeAllDialog,
} = upgradeAllSlice.actions;
export default upgradeAllSlice.reducer;

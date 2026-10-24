import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

// PIN_LATEST_STABLE_TARGET must be non-empty (Radix Select treats '' as no
// selection) and not version-shaped; pinVersionThunks maps it back to ''.
export const PIN_LATEST_STABLE_TARGET = '__erun_pin_latest_stable__';

// PinSiteView is one place the erun version is recorded; all must agree.
export interface PinSiteView {
  kind: string;
  label: string;
  current: string;
  target: string;
  aligned: boolean;
}

export interface PinPlanView {
  tenant: string;
  environment: string;
  target: string;
  previous?: string;
  sites: PinSiteView[];
  // skipped lists references on another release line, not erun's to move.
  skipped?: string[];
  changed: number;
  aligned: boolean;
}

export interface PinVersionState {
  open: boolean;
  selection: UISelection | null;
  available: string[];
  target: string;
  loadingVersions: boolean;
  previewing: boolean;
  applying: boolean;
  plan: PinPlanView | null;
  applied: boolean;
  error: string;
  status: string;
  // checkoutResolvable is optimistic until the check returns.
  checkoutResolvable: boolean;
  checkoutReason: string;
}

const initialState: PinVersionState = {
  open: false,
  selection: null,
  available: [],
  target: PIN_LATEST_STABLE_TARGET,
  loadingVersions: false,
  previewing: false,
  applying: false,
  plan: null,
  applied: false,
  error: '',
  status: '',
  checkoutResolvable: true,
  checkoutReason: '',
};

export const pinVersionSlice = createSlice({
  name: 'pinVersion',
  initialState,
  reducers: {
    openPinVersionDialog(state, action: PayloadAction<UISelection>) {
      Object.assign(state, initialState);
      state.open = true;
      state.selection = action.payload;
      state.loadingVersions = true;
    },
    setPinVersions(state, action: PayloadAction<string[]>) {
      state.loadingVersions = false;
      state.available = action.payload;
    },
    setPinVersionsError(state, action: PayloadAction<string>) {
      state.loadingVersions = false;
      state.error = action.payload;
    },
    setPinTarget(state, action: PayloadAction<string>) {
      state.target = action.payload;
      // A plan is for one target; clear it so the wrong one cannot be applied.
      state.plan = null;
      state.applied = false;
      state.status = '';
    },
    setPinPreviewing(state, action: PayloadAction<boolean>) {
      state.previewing = action.payload;
      if (action.payload) {
        state.error = '';
        state.status = '';
      }
    },
    setPinApplying(state, action: PayloadAction<boolean>) {
      state.applying = action.payload;
      if (action.payload) {
        state.error = '';
        state.status = '';
      }
    },
    setPinPlan(state, action: PayloadAction<{ plan: PinPlanView; applied: boolean }>) {
      state.previewing = false;
      state.applying = false;
      state.plan = action.payload.plan;
      state.applied = action.payload.applied;
      state.error = '';
      if (action.payload.applied) {
        state.status = action.payload.plan.aligned
          ? 'Already aligned — nothing changed.'
          : `Pinned to ${action.payload.plan.target}. Nothing is deployed yet.`;
      }
    },
    setPinError(state, action: PayloadAction<string>) {
      state.previewing = false;
      state.applying = false;
      state.error = action.payload;
    },
    setPinRepoCheckoutStatus(
      state,
      action: PayloadAction<{ resolvable: boolean; reason: string }>,
    ) {
      state.checkoutResolvable = action.payload.resolvable;
      state.checkoutReason = action.payload.reason;
    },
    closePinVersionDialog() {
      return initialState;
    },
  },
});

export const {
  openPinVersionDialog,
  setPinVersions,
  setPinVersionsError,
  setPinTarget,
  setPinPreviewing,
  setPinApplying,
  setPinPlan,
  setPinError,
  setPinRepoCheckoutStatus,
  closePinVersionDialog,
} = pinVersionSlice.actions;
export default pinVersionSlice.reducer;

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

// One erun version is recorded in several places — the Terraform refs, each
// umbrella's erun chart dependencies, the build-env image tag, the environment's
// own runtime version — and they only work when they agree. This dialog moves
// them together, and shows the plan first: a re-pin edits files across a repo,
// so it should be something an operator agrees to rather than trusts.
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
  // applied marks the plan as the outcome of a real run rather than a preview,
  // so the dialog reports what changed instead of what would.
  applied: boolean;
  error: string;
  status: string;
}

const initialState: PinVersionState = {
  open: false,
  selection: null,
  available: [],
  target: '',
  loadingVersions: false,
  previewing: false,
  applying: false,
  plan: null,
  applied: false,
  error: '',
  status: '',
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
      // A plan describes one target, so it stops being an answer the moment the
      // target changes — keeping it on screen would invite applying the wrong one.
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
  closePinVersionDialog,
} = pinVersionSlice.actions;
export default pinVersionSlice.reducer;

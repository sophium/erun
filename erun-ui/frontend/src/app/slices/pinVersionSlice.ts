import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

// PIN_LATEST_STABLE_TARGET is the Version select's "no explicit choice" option.
// It has to be a real, non-empty value: Radix Select reads an empty SelectItem
// value as invalid and treats '' as "nothing selected" (SelectField passes
// `value || undefined` to the underlying Select), which is exactly why the
// trigger rendered blank instead of "Latest stable". It is deliberately not a
// version-shaped string, since the registry can publish a literal `latest`
// tag (docker's own convention) and this must never collide with that.
// pinVersionThunks maps it back to '' at the Go call boundary, where an empty
// target has always meant "resolve the latest published stable release".
export const PIN_LATEST_STABLE_TARGET = '__erun_pin_latest_stable__';

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
  // checkoutResolvable is optimistic (true) until the check comes back, so the
  // dialog does not flash a blocking notice for the common case while it
  // loads. checkoutReason explains a false result — a sourceless runtime
  // environment with no known checkout of its repo on this machine.
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

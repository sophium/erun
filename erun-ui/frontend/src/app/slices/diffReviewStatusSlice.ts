import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIDiffReviewStatus } from '@/types';

// DiffReviewStatusSlot is one environment section's review-status chip state.
export interface DiffReviewStatusSlot {
  loading: boolean;
  status: UIDiffReviewStatus;
  error: string;
}

export const emptyDiffReviewStatusSlot: DiffReviewStatusSlot = {
  loading: false,
  status: { state: 'checking', canAdvanceMergeQueue: false },
  error: '',
};

export interface DiffReviewStatusState {
  byEnv: Record<string, DiffReviewStatusSlot | undefined>;
}

const initialState: DiffReviewStatusState = { byEnv: {} };

function slot(state: DiffReviewStatusState, envKey: string): DiffReviewStatusSlot {
  const existing = state.byEnv[envKey];
  if (existing) {
    return existing;
  }
  const created = { ...emptyDiffReviewStatusSlot };
  state.byEnv[envKey] = created;
  return created;
}

const diffReviewStatusSlice = createSlice({
  name: 'diffReviewStatus',
  initialState,
  reducers: {
    setDiffReviewStatusLoading(state, action: PayloadAction<{ envKey: string }>) {
      slot(state, action.payload.envKey).loading = true;
    },
    setDiffReviewStatus(
      state,
      action: PayloadAction<{ envKey: string; status: UIDiffReviewStatus }>,
    ) {
      state.byEnv[action.payload.envKey] = {
        loading: false,
        status: action.payload.status,
        error: '',
      };
    },
    setDiffReviewStatusError(state, action: PayloadAction<{ envKey: string; error: string }>) {
      state.byEnv[action.payload.envKey] = {
        loading: false,
        status: { state: 'unavailable', canAdvanceMergeQueue: false },
        error: action.payload.error,
      };
    },
    pruneDiffReviewStatuses(state, action: PayloadAction<string[]>) {
      const keep = new Set(action.payload);
      state.byEnv = Object.fromEntries(
        Object.entries(state.byEnv).filter(([key]) => keep.has(key)),
      );
    },
  },
});

export const {
  setDiffReviewStatusLoading,
  setDiffReviewStatus,
  setDiffReviewStatusError,
  pruneDiffReviewStatuses,
} = diffReviewStatusSlice.actions;

export default diffReviewStatusSlice.reducer;

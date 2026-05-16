import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { ReconnectState } from '../state';
import type { DiffResult } from '@/types';

export interface ReviewState {
  diff: DiffResult | null;
  diffLoading: boolean;
  diffError: string;
  diffErrorReconnectable: boolean;
  reconnect: ReconnectState;
  selectedDiffPath: string;
  selectedReviewScope: 'current' | 'commit' | 'all';
  selectedReviewCommit: string;
  diffFilter: string;
  collapsedDiffDirs: string[];
}

const initialState: ReviewState = {
  diff: null,
  diffLoading: false,
  diffError: '',
  diffErrorReconnectable: false,
  reconnect: { status: 'idle', lastLine: '', error: '' },
  selectedDiffPath: '',
  selectedReviewScope: 'current',
  selectedReviewCommit: '',
  diffFilter: '',
  collapsedDiffDirs: [],
};

export const reviewSlice = createSlice({
  name: 'review',
  initialState,
  reducers: {
    setDiff(state, action: PayloadAction<DiffResult | null>) {
      state.diff = action.payload;
    },
    setDiffLoading(state, action: PayloadAction<boolean>) {
      state.diffLoading = action.payload;
    },
    setDiffError(state, action: PayloadAction<{ error: string; reconnectable: boolean }>) {
      state.diffError = action.payload.error;
      state.diffErrorReconnectable = action.payload.reconnectable;
    },
    setReconnect(state, action: PayloadAction<ReconnectState>) {
      state.reconnect = action.payload;
    },
    setSelectedDiffPath(state, action: PayloadAction<string>) {
      state.selectedDiffPath = action.payload;
    },
    setSelectedReviewScope(state, action: PayloadAction<ReviewState['selectedReviewScope']>) {
      state.selectedReviewScope = action.payload;
    },
    setSelectedReviewCommit(state, action: PayloadAction<string>) {
      state.selectedReviewCommit = action.payload;
    },
    setDiffFilter(state, action: PayloadAction<string>) {
      state.diffFilter = action.payload;
    },
    toggleDiffDirCollapsed(state, action: PayloadAction<string>) {
      const idx = state.collapsedDiffDirs.indexOf(action.payload);
      if (idx >= 0) {
        state.collapsedDiffDirs.splice(idx, 1);
      } else {
        state.collapsedDiffDirs.push(action.payload);
      }
    },
    clearDiffDirsCollapsed(state) {
      state.collapsedDiffDirs = [];
    },
    setAll(_state, action: PayloadAction<ReviewState>) {
      return action.payload;
    },
  },
});

export const {
  setDiff,
  setDiffLoading,
  setDiffError,
  setReconnect,
  setSelectedDiffPath,
  setSelectedReviewScope,
  setSelectedReviewCommit,
  setDiffFilter,
  toggleDiffDirCollapsed,
  clearDiffDirsCollapsed,
  setAll: setReviewAll,
} = reviewSlice.actions;
export default reviewSlice.reducer;

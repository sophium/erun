import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { DiffResult } from '@/types';

import { RECONNECT_LINE_BUFFER_LIMIT, type ReconnectState } from '../state';

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
  reconnect: { status: 'idle', tenant: '', environment: '', lines: [], error: '' },
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
    appendReconnectLine(state, action: PayloadAction<string>) {
      state.reconnect.lines.push(action.payload);
      if (state.reconnect.lines.length > RECONNECT_LINE_BUFFER_LIMIT) {
        state.reconnect.lines.splice(0, state.reconnect.lines.length - RECONNECT_LINE_BUFFER_LIMIT);
      }
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
  appendReconnectLine,
  setSelectedDiffPath,
  setSelectedReviewScope,
  setSelectedReviewCommit,
  setDiffFilter,
  toggleDiffDirCollapsed,
  clearDiffDirsCollapsed,
  setAll: setReviewAll,
} = reviewSlice.actions;
export default reviewSlice.reducer;

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import {
  DEFAULT_DEBUG_HEIGHT,
  DEFAULT_FILES_WIDTH,
  DEFAULT_REVIEW_WIDTH,
  DEFAULT_SIDEBAR_WIDTH,
} from '../state';
import {
  loadSavedDebugHeight,
  loadSavedDebugOpen,
  loadSavedFilesOpen,
  loadSavedFilesWidth,
  loadSavedReviewWidth,
  loadSavedSidebarWidth,
} from '../storage';

export interface LayoutState {
  sidebarWidth: number;
  reviewWidth: number;
  filesWidth: number;
  filesOpen: boolean;
  sidebarHidden: boolean;
  reviewOpen: boolean;
  changedFilesOpen: boolean;
  debugOpen: boolean;
  debugHeight: number;
}

const initialState: LayoutState = {
  sidebarWidth: typeof window !== 'undefined' ? loadSavedSidebarWidth() : DEFAULT_SIDEBAR_WIDTH,
  reviewWidth: typeof window !== 'undefined' ? loadSavedReviewWidth() : DEFAULT_REVIEW_WIDTH,
  filesWidth: typeof window !== 'undefined' ? loadSavedFilesWidth() : DEFAULT_FILES_WIDTH,
  filesOpen: typeof window !== 'undefined' ? loadSavedFilesOpen() : true,
  sidebarHidden: false,
  reviewOpen: false,
  changedFilesOpen: true,
  debugOpen: typeof window !== 'undefined' ? loadSavedDebugOpen() : false,
  debugHeight: typeof window !== 'undefined' ? loadSavedDebugHeight() : DEFAULT_DEBUG_HEIGHT,
};

export const layoutSlice = createSlice({
  name: 'layout',
  initialState,
  reducers: {
    setSidebarWidth(state, action: PayloadAction<number>) {
      state.sidebarWidth = action.payload;
    },
    setReviewWidth(state, action: PayloadAction<number>) {
      state.reviewWidth = action.payload;
    },
    setFilesWidth(state, action: PayloadAction<number>) {
      state.filesWidth = action.payload;
    },
    setDebugHeight(state, action: PayloadAction<number>) {
      state.debugHeight = action.payload;
    },
    setSidebarHidden(state, action: PayloadAction<boolean>) {
      state.sidebarHidden = action.payload;
    },
    setReviewOpen(state, action: PayloadAction<boolean>) {
      state.reviewOpen = action.payload;
    },
    setFilesOpen(state, action: PayloadAction<boolean>) {
      state.filesOpen = action.payload;
    },
    setChangedFilesOpen(state, action: PayloadAction<boolean>) {
      state.changedFilesOpen = action.payload;
    },
    setDebugOpen(state, action: PayloadAction<boolean>) {
      state.debugOpen = action.payload;
    },
    setAll(_state, action: PayloadAction<LayoutState>) {
      return action.payload;
    },
  },
});

export const {
  setSidebarWidth,
  setReviewWidth,
  setFilesWidth,
  setDebugHeight,
  setSidebarHidden,
  setReviewOpen,
  setFilesOpen,
  setChangedFilesOpen,
  setDebugOpen,
  setAll: setLayoutAll,
} = layoutSlice.actions;

export default layoutSlice.reducer;

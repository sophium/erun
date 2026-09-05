import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { SidebarUserOverride } from '../state';
import {
  DEFAULT_DEBUG_HEIGHT,
  DEFAULT_FILES_WIDTH,
  DEFAULT_REVIEW_WIDTH,
  DEFAULT_SIDEBAR_WIDTH,
  nextSidebarHidden,
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
  // Distinct from sidebarHidden so a viewport resize can drive the boolean
  // without erasing (or being blocked by) a deliberate operator toggle.
  sidebarUserOverride: SidebarUserOverride;
  reviewOpen: boolean;
  changedFilesOpen: boolean;
  debugOpen: boolean;
  debugHeight: number;
  activityQueueOpen: boolean;
}

const initialState: LayoutState = {
  sidebarWidth: typeof window !== 'undefined' ? loadSavedSidebarWidth() : DEFAULT_SIDEBAR_WIDTH,
  reviewWidth: typeof window !== 'undefined' ? loadSavedReviewWidth() : DEFAULT_REVIEW_WIDTH,
  filesWidth: typeof window !== 'undefined' ? loadSavedFilesWidth() : DEFAULT_FILES_WIDTH,
  filesOpen: typeof window !== 'undefined' ? loadSavedFilesOpen() : true,
  // Seed the initial collapse from the window's own launch width so a narrow
  // launch renders collapsed on the first paint instead of flashing open and
  // then collapsing once the resize-driven reconciliation catches up.
  sidebarHidden:
    typeof window !== 'undefined' ? nextSidebarHidden(false, null, window.innerWidth) : false,
  sidebarUserOverride: null,
  reviewOpen: false,
  changedFilesOpen: true,
  debugOpen: typeof window !== 'undefined' ? loadSavedDebugOpen() : false,
  debugHeight: typeof window !== 'undefined' ? loadSavedDebugHeight() : DEFAULT_DEBUG_HEIGHT,
  activityQueueOpen: false,
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
    setSidebarUserOverride(state, action: PayloadAction<'shown' | 'hidden'>) {
      state.sidebarUserOverride = action.payload;
      state.sidebarHidden = action.payload === 'hidden';
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
    setActivityQueueOpen(state, action: PayloadAction<boolean>) {
      state.activityQueueOpen = action.payload;
    },
  },
});

export const {
  setSidebarWidth,
  setReviewWidth,
  setFilesWidth,
  setDebugHeight,
  setSidebarHidden,
  setSidebarUserOverride,
  setReviewOpen,
  setFilesOpen,
  setChangedFilesOpen,
  setDebugOpen,
  setActivityQueueOpen,
} = layoutSlice.actions;

export default layoutSlice.reducer;

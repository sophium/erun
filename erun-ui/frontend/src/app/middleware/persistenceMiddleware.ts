import { createListenerMiddleware, isAnyOf } from '@reduxjs/toolkit';

import {
  setDebugHeight,
  setDebugOpen,
  setFilesOpen,
  setFilesWidth,
  setReviewWidth,
  setSidebarWidth,
} from '../slices/layoutSlice';
import {
  DEBUG_HEIGHT_STORAGE_KEY,
  DEBUG_OPEN_STORAGE_KEY,
  FILES_OPEN_STORAGE_KEY,
  FILES_WIDTH_STORAGE_KEY,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
} from '../state';
import { saveBoolean, saveNumber } from '../storage';
import type { AppDispatch, RootState } from '../store';

export const persistenceMiddleware = createListenerMiddleware();

const startListening = persistenceMiddleware.startListening.withTypes<RootState, AppDispatch>();

startListening({
  matcher: isAnyOf(setSidebarWidth, setReviewWidth, setFilesWidth, setDebugHeight),
  effect: (action) => {
    if (setSidebarWidth.match(action)) saveNumber(SIDEBAR_WIDTH_STORAGE_KEY, action.payload);
    else if (setReviewWidth.match(action)) saveNumber(REVIEW_WIDTH_STORAGE_KEY, action.payload);
    else if (setFilesWidth.match(action)) saveNumber(FILES_WIDTH_STORAGE_KEY, action.payload);
    else if (setDebugHeight.match(action)) saveNumber(DEBUG_HEIGHT_STORAGE_KEY, action.payload);
  },
});

startListening({
  matcher: isAnyOf(setFilesOpen, setDebugOpen),
  effect: (action) => {
    if (setFilesOpen.match(action)) saveBoolean(FILES_OPEN_STORAGE_KEY, action.payload);
    else if (setDebugOpen.match(action)) saveBoolean(DEBUG_OPEN_STORAGE_KEY, action.payload);
  },
});

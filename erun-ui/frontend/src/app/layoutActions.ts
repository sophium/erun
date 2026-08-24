import type * as React from 'react';

import {
  setChangedFilesOpen as setChangedFilesOpenAction,
  setDebugHeight,
  setDebugOpen as setDebugOpenAction,
  setFilesOpen as setFilesOpenAction,
  setFilesWidth,
  setReviewOpen as setReviewOpenAction,
  setReviewWidth,
  setSidebarHidden,
  setSidebarWidth,
} from './slices/layoutSlice';
import {
  computeMaxReviewWidth,
  DEBUG_HEIGHT_STORAGE_KEY,
  DEBUG_OPEN_STORAGE_KEY,
  FILES_OPEN_STORAGE_KEY,
  FILES_WIDTH_STORAGE_KEY,
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MAX_SIDEBAR_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  MIN_SIDEBAR_WIDTH,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
} from './state';
import { clamp, saveBoolean, saveNumber } from './storage';
import type { AppDispatch, RootState } from './store';

interface LayoutCallbacks {
  applyLayoutVars: () => void;
  focusTerminalSoon: () => void;
  queueTerminalResize: () => void;
  flushTerminalResize: () => void;
}

export function toggleSidebar(
  dispatch: AppDispatch,
  getState: () => RootState,
  callbacks: LayoutCallbacks,
): void {
  dispatch(setSidebarHidden(!getState().layout.sidebarHidden));
  callbacks.applyLayoutVars();
  callbacks.flushTerminalResize();
  callbacks.focusTerminalSoon();
}

export function startSidebarResize(
  dispatch: AppDispatch,
  getState: () => RootState,
  event: React.MouseEvent<HTMLElement>,
  applyLayoutVars: () => void,
): void {
  if (getState().layout.sidebarHidden) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing');

  const move = (moveEvent: MouseEvent) => {
    dispatch(setSidebarWidth(clamp(moveEvent.clientX, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH)));
    applyLayoutVars();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(SIDEBAR_WIDTH_STORAGE_KEY, getState().layout.sidebarWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function stepSidebarWidth(
  dispatch: AppDispatch,
  getState: () => RootState,
  delta: number,
  applyLayoutVars: () => void,
): void {
  if (getState().layout.sidebarHidden) {
    return;
  }
  const next = clamp(getState().layout.sidebarWidth + delta, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
  dispatch(setSidebarWidth(next));
  applyLayoutVars();
  saveNumber(SIDEBAR_WIDTH_STORAGE_KEY, next);
}

export function startReviewResize(
  dispatch: AppDispatch,
  getState: () => RootState,
  event: React.MouseEvent<HTMLElement>,
  terminalPane: HTMLElement | null,
  callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'queueTerminalResize'>,
): void {
  if (!getState().layout.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-review');

  const move = (moveEvent: MouseEvent) => {
    const paneRect = terminalPane?.getBoundingClientRect();
    if (!paneRect) {
      return;
    }
    const layout = getState().layout;
    const effectiveSidebar = layout.sidebarHidden ? 0 : layout.sidebarWidth;
    const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
    dispatch(setReviewWidth(clamp(paneRect.right - moveEvent.clientX, MIN_REVIEW_WIDTH, maxWidth)));
    callbacks.applyLayoutVars();
    callbacks.queueTerminalResize();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-review');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(REVIEW_WIDTH_STORAGE_KEY, getState().layout.reviewWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function stepReviewWidth(
  dispatch: AppDispatch,
  getState: () => RootState,
  delta: number,
  callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'queueTerminalResize'>,
): void {
  const layout = getState().layout;
  if (!layout.reviewOpen) {
    return;
  }
  const effectiveSidebar = layout.sidebarHidden ? 0 : layout.sidebarWidth;
  const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
  const next = clamp(layout.reviewWidth + delta, MIN_REVIEW_WIDTH, maxWidth);
  dispatch(setReviewWidth(next));
  callbacks.applyLayoutVars();
  callbacks.queueTerminalResize();
  saveNumber(REVIEW_WIDTH_STORAGE_KEY, next);
}

export function startFilesResize(
  dispatch: AppDispatch,
  getState: () => RootState,
  event: React.MouseEvent<HTMLElement>,
  reviewView: HTMLElement | null,
  applyLayoutVars: () => void,
): void {
  if (!getState().layout.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-files');

  const move = (moveEvent: MouseEvent) => {
    const reviewRect = reviewView?.getBoundingClientRect();
    if (!reviewRect) {
      return;
    }
    dispatch(
      setFilesWidth(clamp(reviewRect.right - moveEvent.clientX, MIN_FILES_WIDTH, MAX_FILES_WIDTH)),
    );
    applyLayoutVars();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-files');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(FILES_WIDTH_STORAGE_KEY, getState().layout.filesWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function stepFilesWidth(
  dispatch: AppDispatch,
  getState: () => RootState,
  delta: number,
  applyLayoutVars: () => void,
): void {
  if (!getState().layout.reviewOpen) {
    return;
  }
  const next = clamp(getState().layout.filesWidth + delta, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
  dispatch(setFilesWidth(next));
  applyLayoutVars();
  saveNumber(FILES_WIDTH_STORAGE_KEY, next);
}

export function startDebugResize(
  dispatch: AppDispatch,
  getState: () => RootState,
  event: React.MouseEvent<HTMLElement>,
  terminalPane: HTMLElement | null,
  callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'queueTerminalResize'>,
): void {
  if (!getState().layout.debugOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-debug');

  const move = (moveEvent: MouseEvent) => {
    const paneRect = terminalPane?.getBoundingClientRect();
    if (!paneRect) {
      return;
    }
    const maxForPane = Math.max(
      MIN_DEBUG_HEIGHT,
      Math.min(MAX_DEBUG_HEIGHT, paneRect.height - 120),
    );
    dispatch(
      setDebugHeight(clamp(paneRect.bottom - moveEvent.clientY, MIN_DEBUG_HEIGHT, maxForPane)),
    );
    callbacks.applyLayoutVars();
    callbacks.queueTerminalResize();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-debug');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(DEBUG_HEIGHT_STORAGE_KEY, getState().layout.debugHeight);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function stepDebugHeight(
  dispatch: AppDispatch,
  getState: () => RootState,
  delta: number,
  terminalPane: HTMLElement | null,
  callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'queueTerminalResize'>,
): void {
  if (!getState().layout.debugOpen) {
    return;
  }
  const paneRect = terminalPane?.getBoundingClientRect();
  const maxForPane = paneRect
    ? Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, paneRect.height - 120))
    : MAX_DEBUG_HEIGHT;
  const next = clamp(getState().layout.debugHeight + delta, MIN_DEBUG_HEIGHT, maxForPane);
  dispatch(setDebugHeight(next));
  callbacks.applyLayoutVars();
  callbacks.queueTerminalResize();
  saveNumber(DEBUG_HEIGHT_STORAGE_KEY, next);
}

export function toggleReview(
  dispatch: AppDispatch,
  getState: () => RootState,
  callbacks: LayoutCallbacks & { loadReviewDiff: () => void },
): void {
  const next = !getState().layout.reviewOpen;
  dispatch(setReviewOpenAction(next));
  callbacks.applyLayoutVars();
  setFilesOpen(dispatch, getState, getState().layout.filesOpen, false, callbacks.applyLayoutVars);
  // Refit immediately: a debounced resize lets shell output land at the old
  // cols and stick in scrollback before the PTY learns the new width.
  callbacks.flushTerminalResize();
  if (next) {
    callbacks.loadReviewDiff();
  }
  callbacks.focusTerminalSoon();
}

export function setFilesOpen(
  dispatch: AppDispatch,
  _getState: () => RootState,
  open: boolean,
  persist: boolean,
  applyLayoutVars: () => void,
): void {
  dispatch(setFilesOpenAction(open));
  applyLayoutVars();
  if (persist) {
    saveBoolean(FILES_OPEN_STORAGE_KEY, open);
  }
}

export function setDebugOpen(
  dispatch: AppDispatch,
  open: boolean,
  flushTerminalResize: () => void,
): void {
  dispatch(setDebugOpenAction(open));
  saveBoolean(DEBUG_OPEN_STORAGE_KEY, open);
  flushTerminalResize();
}

export { setChangedFilesOpenAction as setChangedFilesOpen };

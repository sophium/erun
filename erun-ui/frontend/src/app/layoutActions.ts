import type * as React from 'react';
import {
  DEBUG_HEIGHT_STORAGE_KEY,
  DEBUG_OPEN_STORAGE_KEY,
  FILES_OPEN_STORAGE_KEY,
  FILES_WIDTH_STORAGE_KEY,
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MAX_REVIEW_WIDTH,
  MAX_SIDEBAR_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  MIN_SIDEBAR_WIDTH,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
  type AppState,
} from './state';
import { clamp, saveBoolean, saveNumber } from './storage';

type LayoutCallbacks = {
  applyLayoutVars: () => void;
  emit: () => void;
  focusTerminalSoon: () => void;
  queueTerminalResize: () => void;
};

export function toggleSidebar(state: AppState, callbacks: LayoutCallbacks): void {
  state.sidebarHidden = !state.sidebarHidden;
  callbacks.applyLayoutVars();
  callbacks.emit();
  callbacks.queueTerminalResize();
  callbacks.focusTerminalSoon();
}

export function startSidebarResize(state: AppState, event: React.MouseEvent<HTMLElement>, applyLayoutVars: () => void, emit: () => void): void {
  if (state.sidebarHidden) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing');

  const move = (moveEvent: MouseEvent) => {
    state.sidebarWidth = clamp(moveEvent.clientX, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
    applyLayoutVars();
    emit();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(SIDEBAR_WIDTH_STORAGE_KEY, state.sidebarWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function startReviewResize(state: AppState, event: React.MouseEvent<HTMLElement>, terminalPane: HTMLElement | null, callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'emit' | 'queueTerminalResize'>): void {
  if (!state.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-review');

  const move = (moveEvent: MouseEvent) => {
    const paneRect = terminalPane?.getBoundingClientRect();
    if (!paneRect) {
      return;
    }
    state.reviewWidth = clamp(paneRect.right - moveEvent.clientX, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
    callbacks.applyLayoutVars();
    callbacks.emit();
    callbacks.queueTerminalResize();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-review');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(REVIEW_WIDTH_STORAGE_KEY, state.reviewWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function startFilesResize(state: AppState, event: React.MouseEvent<HTMLElement>, reviewView: HTMLElement | null, applyLayoutVars: () => void, emit: () => void): void {
  if (!state.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-files');

  const move = (moveEvent: MouseEvent) => {
    const reviewRect = reviewView?.getBoundingClientRect();
    if (!reviewRect) {
      return;
    }
    state.filesWidth = clamp(reviewRect.right - moveEvent.clientX, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
    applyLayoutVars();
    emit();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-files');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(FILES_WIDTH_STORAGE_KEY, state.filesWidth);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function startDebugResize(state: AppState, event: React.MouseEvent<HTMLElement>, terminalPane: HTMLElement | null, callbacks: Pick<LayoutCallbacks, 'applyLayoutVars' | 'emit' | 'queueTerminalResize'>): void {
  if (!state.debugOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-debug');

  const move = (moveEvent: MouseEvent) => {
    const paneRect = terminalPane?.getBoundingClientRect();
    if (!paneRect) {
      return;
    }
    const maxForPane = Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, paneRect.height - 120));
    state.debugHeight = clamp(paneRect.bottom - moveEvent.clientY, MIN_DEBUG_HEIGHT, maxForPane);
    callbacks.applyLayoutVars();
    callbacks.emit();
    callbacks.queueTerminalResize();
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-debug');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveNumber(DEBUG_HEIGHT_STORAGE_KEY, state.debugHeight);
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
}

export function toggleReview(state: AppState, callbacks: LayoutCallbacks & { loadReviewDiff: () => void }): void {
  state.reviewOpen = !state.reviewOpen;
  callbacks.applyLayoutVars();
  setFilesOpen(state, state.filesOpen, false, callbacks.applyLayoutVars, callbacks.emit);
  callbacks.emit();
  callbacks.queueTerminalResize();
  if (state.reviewOpen) {
    callbacks.loadReviewDiff();
  }
  callbacks.focusTerminalSoon();
}

export function setFilesOpen(state: AppState, open: boolean, persist: boolean, applyLayoutVars: () => void, emit: () => void): void {
  state.filesOpen = open;
  applyLayoutVars();
  if (persist) {
    saveBoolean(FILES_OPEN_STORAGE_KEY, open);
  }
  emit();
}

export function setDebugOpen(state: AppState, open: boolean, emit: () => void, queueTerminalResize: () => void): void {
  state.debugOpen = open;
  saveBoolean(DEBUG_OPEN_STORAGE_KEY, open);
  if (open && !state.debugOutput) {
    state.debugOutput = 'Debug output will appear here for new erun sessions started while this panel is open.\n';
  }
  emit();
  queueTerminalResize();
}

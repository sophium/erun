import {
  computeMaxReviewWidth,
  effectiveSidebarWidth,
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
} from './state';
import { clamp } from './storage';
import { store } from './store';

export interface LayoutVarElements {
  reviewView: HTMLElement | null;
  terminalPane: HTMLElement | null;
}

// Publishes the runtime CSS variables the shell reads for its resizable panels,
// clamping each stored width/height to the space currently available.
export function applyTerminalLayoutVars(elements: LayoutVarElements): void {
  const root = document.documentElement;
  const layout = store.getState().layout;
  const sidebarPx = effectiveSidebarWidth(
    layout.sidebarHidden,
    layout.sidebarWidth,
    window.innerWidth,
  );
  root.style.setProperty('--sidebar-width', `${String(sidebarPx)}px`);
  root.style.setProperty('--review-width', `${String(clampedReviewWidth())}px`);
  root.style.setProperty('--files-width', `${String(clampedFilesWidth(elements.reviewView))}px`);
  root.style.setProperty(
    '--debug-height',
    `${String(clampedDebugHeight(elements.terminalPane))}px`,
  );
}

function clampedReviewWidth(): number {
  const layout = store.getState().layout;
  const effectiveSidebar = effectiveSidebarWidth(
    layout.sidebarHidden,
    layout.sidebarWidth,
    window.innerWidth,
  );
  const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
  return clamp(layout.reviewWidth, MIN_REVIEW_WIDTH, maxWidth);
}

function clampedFilesWidth(reviewView: HTMLElement | null): number {
  const layout = store.getState().layout;
  const reviewWidth = reviewView?.getBoundingClientRect().width ?? layout.reviewWidth;
  const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
  return clamp(
    layout.filesWidth,
    MIN_FILES_WIDTH,
    Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview)),
  );
}

function clampedDebugHeight(terminalPane: HTMLElement | null): number {
  const paneHeight = terminalPane?.getBoundingClientRect().height ?? 0;
  const maxDebugForPane = paneHeight > 0 ? paneHeight - 120 : MAX_DEBUG_HEIGHT;
  return clamp(
    store.getState().layout.debugHeight,
    MIN_DEBUG_HEIGHT,
    Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, maxDebugForPane)),
  );
}

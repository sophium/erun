import {
  DEFAULT_FILES_WIDTH,
  DEFAULT_DEBUG_HEIGHT,
  DEFAULT_REVIEW_WIDTH,
  DEFAULT_SIDEBAR_WIDTH,
  DEBUG_HEIGHT_STORAGE_KEY,
  DEBUG_OPEN_STORAGE_KEY,
  FILES_OPEN_STORAGE_KEY,
  FILES_WIDTH_STORAGE_KEY,
  MAX_FILES_WIDTH,
  MAX_DEBUG_HEIGHT,
  MAX_REVIEW_WIDTH,
  MAX_SIDEBAR_WIDTH,
  MIN_FILES_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_REVIEW_WIDTH,
  MIN_SIDEBAR_WIDTH,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
} from './state';

export function loadSavedSidebarWidth(): number {
  return loadSavedNumber(SIDEBAR_WIDTH_STORAGE_KEY, DEFAULT_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
}

export function loadSavedReviewWidth(): number {
  return loadSavedNumber(REVIEW_WIDTH_STORAGE_KEY, DEFAULT_REVIEW_WIDTH, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
}

export function loadSavedFilesWidth(): number {
  return loadSavedNumber(FILES_WIDTH_STORAGE_KEY, DEFAULT_FILES_WIDTH, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
}

export function loadSavedDebugHeight(): number {
  return loadSavedNumber(DEBUG_HEIGHT_STORAGE_KEY, DEFAULT_DEBUG_HEIGHT, MIN_DEBUG_HEIGHT, MAX_DEBUG_HEIGHT);
}

export function loadSavedFilesOpen(): boolean {
  try {
    return window.localStorage.getItem(FILES_OPEN_STORAGE_KEY) !== 'false';
  } catch {
    return true;
  }
}

export function loadSavedDebugOpen(): boolean {
  try {
    return window.localStorage.getItem(DEBUG_OPEN_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

export function saveNumber(key: string, value: number): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch {
  }
}

export function saveBoolean(key: string, value: boolean): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch {
  }
}

export function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function loadSavedNumber(key: string, fallback: number, minimum: number, maximum: number): number {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return fallback;
    }
    return clamp(parsed, minimum, maximum);
  } catch {
    return fallback;
  }
}

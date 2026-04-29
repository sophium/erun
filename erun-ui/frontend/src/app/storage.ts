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
  PAST_CONTAINER_REGISTRIES_STORAGE_KEY,
  PAST_ENVIRONMENTS_STORAGE_KEY,
  PAST_TENANTS_STORAGE_KEY,
  REVIEW_WIDTH_STORAGE_KEY,
  SIDEBAR_WIDTH_STORAGE_KEY,
} from './state';

const MAX_SAVED_STRING_LIST_ITEMS = 20;

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

export function loadSavedPastTenants(): string[] {
  return loadSavedStringList(PAST_TENANTS_STORAGE_KEY);
}

export function loadSavedPastEnvironments(): string[] {
  return loadSavedStringList(PAST_ENVIRONMENTS_STORAGE_KEY);
}

export function loadSavedPastContainerRegistries(): string[] {
  return loadSavedStringList(PAST_CONTAINER_REGISTRIES_STORAGE_KEY);
}

export function rememberPastTenant(value: string): void {
  rememberStringListValue(PAST_TENANTS_STORAGE_KEY, value);
}

export function rememberPastEnvironment(value: string): void {
  rememberStringListValue(PAST_ENVIRONMENTS_STORAGE_KEY, value);
}

export function rememberPastContainerRegistry(value: string): void {
  rememberStringListValue(PAST_CONTAINER_REGISTRIES_STORAGE_KEY, value);
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

function loadSavedStringList(key: string): string[] {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return [];
    }
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }
    return uniqueStrings(parsed.filter((value): value is string => typeof value === 'string')).slice(0, MAX_SAVED_STRING_LIST_ITEMS);
  } catch {
    return [];
  }
}

function rememberStringListValue(key: string, value: string): void {
  const normalized = value.trim();
  if (!normalized) {
    return;
  }
  const values = uniqueStrings([normalized, ...loadSavedStringList(key)]).slice(0, MAX_SAVED_STRING_LIST_ITEMS);
  try {
    window.localStorage.setItem(key, JSON.stringify(values));
  } catch {
  }
}

function uniqueStrings(values: string[]): string[] {
  const result: string[] = [];
  const seen = new Set<string>();
  for (const value of values) {
    const normalized = value.trim();
    const key = normalized.toLowerCase();
    if (!normalized || seen.has(key)) {
      continue;
    }
    seen.add(key);
    result.push(normalized);
  }
  return result;
}

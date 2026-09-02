// The localStorage keys the shell persists its layout and preferences under,
// split out of state.ts so that file stays inside its line budget. They are
// pure constants with no dependencies, which makes them the cheapest thing in
// there to move; state.ts re-exports them, so every importer is unaffected.
export const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
export const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
export const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
export const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';
export const DEBUG_OPEN_STORAGE_KEY = 'erun.debugOpen';
// Opt-in, and deliberately OFF by default: xterm's screenReaderMode makes the
// AccessibilityManager rewrite the same hidden helper textarea that captures
// keystrokes, so a focus transition mid-line made the next input event re-emit
// the whole accumulated buffer into the pty (#1335). It stays available for
// anyone who needs it; it must not be imposed on everyone who types.
export const TERMINAL_SCREEN_READER_MODE_STORAGE_KEY = 'erun.terminal.screenReaderMode';
export const DEBUG_HEIGHT_STORAGE_KEY = 'erun.debugHeight';
export const THEME_STORAGE_KEY = 'erun.theme';

export type ThemePreference = 'light' | 'dark';
export const PAST_TENANTS_STORAGE_KEY = 'erun.pastTenants';
export const PAST_ENVIRONMENTS_STORAGE_KEY = 'erun.pastEnvironments';
export const PAST_CONTAINER_REGISTRIES_STORAGE_KEY = 'erun.pastContainerRegistries';

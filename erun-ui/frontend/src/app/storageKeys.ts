// localStorage keys for shell layout and preferences; re-exported from state.ts.
export const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
export const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
export const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
export const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';
export const DEBUG_OPEN_STORAGE_KEY = 'erun.debugOpen';
// Off by default: xterm's screenReaderMode rewrites the input textarea, which
// re-emitted the whole buffer into the pty on a mid-line focus change.
export const TERMINAL_SCREEN_READER_MODE_STORAGE_KEY = 'erun.terminal.screenReaderMode';
export const DEBUG_HEIGHT_STORAGE_KEY = 'erun.debugHeight';
export const THEME_STORAGE_KEY = 'erun.theme';

export type ThemePreference = 'light' | 'dark';
export const PAST_TENANTS_STORAGE_KEY = 'erun.pastTenants';
export const PAST_ENVIRONMENTS_STORAGE_KEY = 'erun.pastEnvironments';
export const PAST_CONTAINER_REGISTRIES_STORAGE_KEY = 'erun.pastContainerRegistries';

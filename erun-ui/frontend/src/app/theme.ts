import type { ThemePreference } from './state';
import { loadSavedTheme, saveTheme } from './storage';

export type { ThemePreference } from './state';

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false;
}

// Resolves a previously chosen preference before falling back to the OS
// preference, so an operator's explicit choice survives a relaunch even if
// their OS theme differs. Mirrors erun-console's shell/theme.ts, the
// reference implementation for this same class-based `.dark` mechanism.
export function initialTheme(): ThemePreference {
  return loadSavedTheme() ?? (systemPrefersDark() ? 'dark' : 'light');
}

export function applyTheme(theme: ThemePreference): void {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  saveTheme(theme);
}

import type { ThemePreference } from './state';
import { loadSavedTheme, saveTheme } from './storage';

export type { ThemePreference } from './state';

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false;
}

export function initialTheme(): ThemePreference {
  return loadSavedTheme() ?? (systemPrefersDark() ? 'dark' : 'light');
}

export function applyTheme(theme: ThemePreference): void {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  saveTheme(theme);
}

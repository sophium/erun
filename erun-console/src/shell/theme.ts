// Class-based `.dark` toggle, the mechanism erun-kit's theme.css uses.

export type ThemePreference = 'light' | 'dark';

const STORAGE_KEY = 'erun.console.theme';

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false;
}

// initialTheme prefers a stored choice over the OS preference.
export function initialTheme(): ThemePreference {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'light' || stored === 'dark') {
    return stored;
  }
  return systemPrefersDark() ? 'dark' : 'light';
}

export function applyTheme(theme: ThemePreference): void {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  localStorage.setItem(STORAGE_KEY, theme);
}

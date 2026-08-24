// Dark mode toggle for the console, using the same class-based `.dark`
// mechanism erun-kit's theme.css ships (and erun-ui/frontend's token scale
// already defines but has no toggle wired to yet) — so a future desktop
// toggle would apply identically.

export type ThemePreference = 'light' | 'dark';

const STORAGE_KEY = 'erun.console.theme';

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
    : false;
}

// initialTheme resolves a previously chosen preference before falling back to
// the OS preference, so an operator's explicit choice survives a reload even
// if their OS theme differs.
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

import * as React from 'react';

import { applyTheme, initialTheme, type ThemePreference } from './theme';

export function useTheme(): { theme: ThemePreference; toggleTheme: () => void } {
  const [theme, setTheme] = React.useState<ThemePreference>(() => initialTheme());

  React.useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  const toggleTheme = React.useCallback(() => {
    setTheme((current) => (current === 'dark' ? 'light' : 'dark'));
  }, []);

  return { theme, toggleTheme };
}

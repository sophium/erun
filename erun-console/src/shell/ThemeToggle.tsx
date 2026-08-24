import { Button, IconTooltip } from 'erun-kit';
import { Moon, Sun } from 'lucide-react';
import type * as React from 'react';

import type { ThemePreference } from './theme';

export function ThemeToggle({
  theme,
  onToggle,
}: {
  theme: ThemePreference;
  onToggle: () => void;
}): React.ReactElement {
  const dark = theme === 'dark';
  const label = dark ? 'Switch to light theme' : 'Switch to dark theme';
  return (
    <IconTooltip label={label}>
      <Button type="button" variant="ghost" size="icon-sm" aria-label={label} onClick={onToggle}>
        {dark ? <Sun /> : <Moon />}
      </Button>
    </IconTooltip>
  );
}

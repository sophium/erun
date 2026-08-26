import { Button, IconTooltip } from 'erun-kit';
import { Moon, Sun } from 'lucide-react';
import * as React from 'react';

import { useTheme } from '@/app/useTheme';

const titlebarButtonClassName =
  'size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px]';

export function ThemeToggle(): React.ReactElement {
  const { theme, toggleTheme } = useTheme();
  const dark = theme === 'dark';
  const label = dark ? 'Switch to light theme' : 'Switch to dark theme';
  return (
    <IconTooltip label={label}>
      <Button
        className={titlebarButtonClassName}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={dark}
        onClick={toggleTheme}
      >
        {dark ? <Sun /> : <Moon />}
      </Button>
    </IconTooltip>
  );
}

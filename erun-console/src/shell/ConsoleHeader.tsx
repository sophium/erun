import { Button } from 'erun-kit';
import { LogOut } from 'lucide-react';
import type * as React from 'react';

import type { ThemePreference } from './theme';
import { ThemeToggle } from './ThemeToggle';

export function ConsoleHeader({
  title,
  identityLabel,
  identityPending,
  theme,
  onToggleTheme,
  onSignOut,
}: {
  title: string;
  identityLabel: string | undefined;
  identityPending: boolean;
  theme: ThemePreference;
  onToggleTheme: () => void;
  onSignOut: () => void;
}): React.ReactElement {
  return (
    <header className="flex h-14 flex-none items-center justify-between gap-3 border-b border-border bg-background px-6">
      <h1 className="truncate text-base font-semibold text-foreground">{title}</h1>
      <div className="flex items-center gap-3">
        {(identityLabel !== undefined || identityPending) && (
          <span className="hidden truncate text-sm text-muted-foreground sm:inline">
            {identityLabel ?? 'Loading…'}
          </span>
        )}
        <ThemeToggle theme={theme} onToggle={onToggleTheme} />
        <Button type="button" variant="outline" size="sm" onClick={onSignOut}>
          <LogOut aria-hidden="true" />
          Sign out
        </Button>
      </div>
    </header>
  );
}

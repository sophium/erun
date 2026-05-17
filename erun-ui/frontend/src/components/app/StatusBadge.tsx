import * as React from 'react';

import { cn } from '@/lib/utils';

import type { StatusBadgeTone } from './StatusBadge.helpers';

const toneClassName: Record<StatusBadgeTone, string> = {
  success: 'border-green-600/35 bg-green-600/10 text-green-700 dark:text-green-400',
  destructive:
    'border-[color-mix(in_oklch,var(--destructive)_35%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-destructive',
  muted: 'border-border bg-muted/40 text-muted-foreground',
};

export function StatusBadge({
  tone,
  label,
  className,
}: {
  tone: StatusBadgeTone;
  label: string;
  className?: string;
}): React.ReactElement {
  return (
    <span
      className={cn(
        'shrink-0 rounded-[calc(var(--radius)-2px)] border px-1.5 py-0.5 text-[11px] leading-none font-medium',
        toneClassName[tone],
        className,
      )}
    >
      {label}
    </span>
  );
}

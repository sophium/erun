import { AlertCircle, AlertTriangle, CheckCircle2, Circle, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { cn } from '@/lib/utils';

import type { StatusBadgeTone } from './StatusBadge.helpers';

const toneClassName: Record<StatusBadgeTone, string> = {
  success: 'border-green-600/35 bg-green-600/10 text-green-700 dark:text-green-400',
  warning: 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400',
  destructive:
    'border-[color-mix(in_oklch,var(--destructive)_35%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-destructive',
  'in-progress': 'border-blue-500/40 bg-blue-500/10 text-blue-700 dark:text-blue-400',
  muted: 'border-border bg-muted/40 text-muted-foreground',
};

const toneIcon: Record<
  StatusBadgeTone,
  { Icon: React.ComponentType<React.SVGProps<SVGSVGElement>>; spin: boolean }
> = {
  success: { Icon: CheckCircle2, spin: false },
  warning: { Icon: AlertTriangle, spin: false },
  destructive: { Icon: AlertCircle, spin: false },
  'in-progress': { Icon: LoaderCircle, spin: true },
  muted: { Icon: Circle, spin: false },
};

export function StatusBadge({
  tone,
  label,
  className,
  showIcon = true,
}: {
  tone: StatusBadgeTone;
  label: string;
  className?: string;
  showIcon?: boolean;
}): React.ReactElement {
  const { Icon, spin } = toneIcon[tone];
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 rounded-[calc(var(--radius)-2px)] border px-1.5 py-0.5 text-[11px] leading-none font-medium',
        toneClassName[tone],
        className,
      )}
    >
      {showIcon && (
        <Icon aria-hidden="true" className={cn('size-3 flex-none', spin && 'animate-spin')} />
      )}
      <span>{label}</span>
    </span>
  );
}

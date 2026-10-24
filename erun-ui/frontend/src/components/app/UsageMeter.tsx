import { cn } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import type { UsageSeverity } from '@/components/app/UsageMeter.helpers';
import { usageSeverity } from '@/components/app/UsageMeter.helpers';

// UsageMeter renders one "X of Y" reading as a role="meter" bar plus its text.
// Severity also carries an icon and a word (WCAG 1.4.1); an undefined percent
// renders no fill, never a zero-width bar.

const FILL_CLASS: Record<UsageSeverity, string> = {
  normal: 'bg-primary',
  warning: 'bg-amber-500 dark:bg-amber-400',
  danger: 'bg-destructive',
};

const TRACK_CLASS: Record<UsageSeverity, string> = {
  normal: 'bg-primary/15',
  warning: 'bg-amber-500/20 dark:bg-amber-400/20',
  danger: 'bg-destructive/20',
};

const SEVERITY_WORD: Record<UsageSeverity, string> = {
  normal: '',
  warning: 'warning',
  danger: 'critical',
};

export function UsageMeter({
  label,
  valueText,
  percent,
  warnAt,
  detail,
}: {
  label: string;
  valueText: string;
  percent: number | undefined;
  warnAt: number | undefined;
  detail?: string;
}): React.ReactElement {
  const severity = usageSeverity(percent, warnAt);
  const measured = percent !== undefined && Number.isFinite(percent);
  const clamped = measured ? Math.max(0, Math.min(100, percent)) : 0;
  const severityWord = SEVERITY_WORD[severity];
  const accessibleName = severityWord ? `${label} (${severityWord})` : label;

  return (
    <div className="grid gap-1">
      <div className="flex items-baseline justify-between gap-2">
        <span className="flex items-center gap-1 text-xs leading-[1.35] text-muted-foreground">
          {label}
          {severity !== 'normal' && (
            <TriangleAlert
              aria-hidden="true"
              className={cn(
                'size-3 shrink-0 self-center',
                severity === 'danger' ? 'text-destructive' : 'text-amber-700 dark:text-amber-400',
              )}
            />
          )}
        </span>
        <span
          className={cn(
            'text-sm leading-[1.35] font-semibold tabular-nums',
            severity === 'danger'
              ? 'text-destructive'
              : severity === 'warning'
                ? 'text-amber-700 dark:text-amber-400'
                : 'text-foreground',
          )}
        >
          {valueText}
        </span>
      </div>
      {measured ? (
        <div
          role="meter"
          aria-label={accessibleName}
          aria-valuenow={Math.round(clamped)}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuetext={valueText}
          className={cn('h-1.5 w-full overflow-hidden rounded-full', TRACK_CLASS[severity])}
        >
          <div
            className={cn(
              'h-full rounded-full transition-[width] duration-300 ease-out',
              FILL_CLASS[severity],
            )}
            style={{ width: `${String(clamped)}%` }}
          />
        </div>
      ) : null}
      {detail ? (
        <span className="text-xs leading-[1.35] text-muted-foreground">{detail}</span>
      ) : null}
    </div>
  );
}

import { cn } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import type { UsageSeverity } from '@/components/app/UsageMeter.helpers';
import { usageSeverity } from '@/components/app/UsageMeter.helpers';

// UsageMeter renders one "X of Y" reading as a proportional bar plus its text.
//
// Why a bar and not just the text it already had: a percentage against a limit
// is a magnitude, and magnitude is the one thing text is worst at. "7.8 GiB of
// 10 GiB (78%)" makes the operator read three numbers and do the division; a
// filled track answers "how close am I" before any of them are read. The text
// stays beside it, because the exact figure is what a slider decision needs.
//
// Severity lives in the FILL, not only in a separate warning line, so a
// threshold crossing is visible at a glance rather than needing the warnings
// below to be read. The unfilled track is a lighter step of the same ramp, so
// the state reads across the whole bar rather than only across the filled part.
//
// Colour is never the only signal (WCAG 1.4.1, and the repo's own
// non-color-only status rule): a crossed threshold also gets an icon, and the
// accessible name carries the severity as a word. The bar itself is
// role="meter" with the ARIA value attributes, so it is not decorative markup
// that a screen reader has to infer meaning from.
//
// A percent of `undefined` means "not measured" and renders NO fill at all --
// never a zero-width bar, which reads as "0%, idle" rather than "unknown".
// That is the same fail-soft contract the reader itself follows.

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
  // valueText is the exact reading ("7.8 GiB of 10 GiB"), or the stated
  // unavailability. It is always rendered; the bar is the supplement.
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
        {/* The figure the operator came for: one step up in size and weight from
            its own label, and tabular so stacked rows align on the decimal. */}
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
            // Transition so a refresh reads as the same bar moving rather than
            // a new one appearing; brief enough not to lag the figure beside it.
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

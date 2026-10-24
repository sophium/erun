import * as React from 'react';

import { DECILE_COUNT, decileFillCount, decileIsAlert } from '@/app/usageDecileStrip';

// DecileStrip fills ceil(pct/10) of ten segments; empty segments are outlined so 0% reads as measured, not chrome.
// `percent` is always measured: an unsampled env renders no strip. aria-hidden, since the value above carries the figure.
export function DecileStrip({ percent }: { percent: number }): React.ReactElement {
  const filled = decileFillCount(percent);
  const alert = decileIsAlert(percent);
  return (
    <span
      aria-hidden="true"
      // Read by Playwright specs.
      data-decile-fill={filled}
      data-decile-alert={alert ? 'true' : 'false'}
      className="flex items-center gap-0.5"
    >
      {Array.from({ length: DECILE_COUNT }, (_, index) => (
        <span
          key={index}
          className={`h-1.5 flex-1 rounded-[1px] ${
            index < filled ? FILLED_SEGMENT_CLASS(alert) : EMPTY_SEGMENT_CLASS
          }`}
        />
      ))}
    </span>
  );
}

// `--border` so the outline tracks the theme.
const EMPTY_SEGMENT_CLASS = 'bg-transparent shadow-[inset_0_0_0_1px_var(--border)]';

function FILLED_SEGMENT_CLASS(alert: boolean): string {
  return alert ? 'bg-amber-500 dark:bg-amber-400' : 'bg-emerald-500';
}

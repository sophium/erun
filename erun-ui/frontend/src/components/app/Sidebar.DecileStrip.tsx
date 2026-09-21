import * as React from 'react';

import { DECILE_COUNT, decileFillCount, decileIsAlert } from '@/app/usageDecileStrip';

// DecileStrip is the graphic half of the usage encoding: ten equal segments
// under a CPU or memory figure, of which `ceil(pct/10)` are filled. It is a
// fourth permitted card element (see the TYPE note in Sidebar.HoverCardRow.tsx)
// -- a graphic, not a type treatment, carrying no text of its own, which is why
// it spends none of the card's size/face/weight axes.
//
// Two details are load-bearing and were the point of the design review that
// settled this encoding:
//
//   - An empty segment is an OUTLINED container (`inset 0 0 0 1px` over a
//     transparent background), never a solid grey block. A solid-grey empty
//     strip reads as a dashed rule -- chrome -- whereas an outlined one reads as
//     an empty gauge. This is the single detail that made a measured 0% legible
//     as "measured, and idle" rather than as a decoration.
//   - A non-zero reading always fills at least one segment; see
//     `decileFillCount` for why `ceil` is what guarantees it.
//
// The strip is `aria-hidden`: the figure it encodes is already rendered as text
// in the value directly above it, so it adds no information a screen reader is
// missing, and labelling ten segments individually would bury the number.
//
// `percent` is always a MEASURED reading. An environment that has never been
// sampled must render its own degraded "no reading yet" text and no strip at
// all -- an empty strip means "measured zero", and conflating the two is the
// defect the usage-persistence work exists to prevent.
export function DecileStrip({ percent }: { percent: number }): React.ReactElement {
  const filled = decileFillCount(percent);
  const alert = decileIsAlert(percent);
  return (
    <span aria-hidden="true" className="flex items-center gap-0.5">
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

// EMPTY_SEGMENT_CLASS is `--border` rather than a fixed grey so the outline
// tracks the theme in both light and dark mode.
const EMPTY_SEGMENT_CLASS = 'bg-transparent shadow-[inset_0_0_0_1px_var(--border)]';

// The filled colour reuses the vocabulary the status glyphs already carry:
// emerald for a healthy reading, amber (the same family as
// HOVER_CARD_ALERT_CLASS) once the reading passes the alert threshold.
function FILLED_SEGMENT_CLASS(alert: boolean): string {
  return alert ? 'bg-amber-500 dark:bg-amber-400' : 'bg-emerald-500';
}

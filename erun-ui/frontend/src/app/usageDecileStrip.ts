// Decile encoding for a usage percentage -- the arithmetic half of the strip
// `DecileStrip` (Sidebar.DecileStrip.tsx) renders, split out so the encoding
// itself is testable without a DOM (this suite renders no components; see
// vite.config.ts's `environment: 'node'`).
//
// The ten buckets are NOT a history: a snapshot carries one current reading and
// its age, never a series, so a strip is "where this reading sits on its own
// scale", not "the last ten samples". Each segment is one tenth (10%) of the
// metric's own ceiling, and the reading fills the buckets it has crossed.

export const DECILE_COUNT = 10;

// The alert threshold is a percentage of the metric's own limit, matching the
// amber threshold the rest of the desktop uses for a resource near its ceiling.
export const DECILE_ALERT_PERCENT = 80;

// decileFillCount is `ceil(pct/10)`, clamped to the strip's ten segments.
//
// `ceil`, not `floor` or a fraction, is what makes a non-zero reading
// unconditionally visible: 0.4% is one segment, never zero. That is load-bearing
// rather than incidental -- an interpolating render (a partial-width segment)
// would let "barely busy" collapse into exactly the picture of "idle", which is
// the distinction the strip exists to draw. Zero fills nothing, and zero is a
// real, available reading rather than a missing one; the caller decides which of
// those it is holding, and only ever calls this with a measured percentage.
export function decileFillCount(percent: number): number {
  if (!Number.isFinite(percent)) {
    return 0;
  }
  const clamped = Math.min(100, Math.max(0, percent));
  return Math.min(DECILE_COUNT, Math.ceil(clamped / 10));
}

// decileIsAlert is the amber condition: strictly above 80%, per the reviewed
// threshold. Colour is never the only signal -- the filled segment count already
// carries the magnitude, so a colour-blind or greyscale render loses nothing.
export function decileIsAlert(percent: number): boolean {
  return Number.isFinite(percent) && percent > DECILE_ALERT_PERCENT;
}

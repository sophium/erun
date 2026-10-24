// Each segment is a tenth of the metric's own ceiling; this is one reading, not a history.

export const DECILE_COUNT = 10;

export const DECILE_ALERT_PERCENT = 80;

// ceil, so any non-zero reading fills at least one segment and never looks idle.
export function decileFillCount(percent: number): number {
  if (!Number.isFinite(percent)) {
    return 0;
  }
  const clamped = Math.min(100, Math.max(0, percent));
  return Math.min(DECILE_COUNT, Math.ceil(clamped / 10));
}

// Colour is never the only signal; the segment count carries the magnitude.
export function decileIsAlert(percent: number): boolean {
  return Number.isFinite(percent) && percent > DECILE_ALERT_PERCENT;
}

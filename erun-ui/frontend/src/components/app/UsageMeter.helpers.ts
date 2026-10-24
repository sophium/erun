export type UsageSeverity = 'normal' | 'warning' | 'danger';

// usageSeverity maps a reading to its meter state. Unmeasured is 'normal' (the
// meter renders no fill), and at or above 100% is always 'danger'.
export function usageSeverity(
  percent: number | undefined,
  warnAt: number | undefined,
): UsageSeverity {
  if (percent === undefined || !Number.isFinite(percent) || warnAt === undefined) {
    return 'normal';
  }
  if (percent >= 100) {
    return 'danger';
  }
  return percent >= warnAt ? 'warning' : 'normal';
}

// Severity classification for UsageMeter, split out of the component file so
// that file exports components only (react-refresh/only-export-components),
// following the StatusBadge / EditableComboField helper split already in use.

export type UsageSeverity = 'normal' | 'warning' | 'danger';

// usageSeverity maps a reading to the state its meter should carry.
//
// `undefined` percent or `undefined` threshold is deliberately 'normal', not a
// warning: an unmeasured field must not raise an alarm it has no evidence for.
// The meter renders no fill at all in that case, so 'normal' never reads as
// "measured and fine".
//
// At or above 100% is 'danger' regardless of the warn threshold -- a container
// at its limit is a different statement from one merely approaching it.
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

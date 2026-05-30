export type StatusBadgeTone = 'success' | 'warning' | 'destructive' | 'in-progress' | 'muted';

// statusTonesByName captures the lookup so cloudProviderStatusTone stays
// within the eslint complexity ceiling and additions are obvious. Keys
// match the canonical cloud-context status strings emitted by the
// backend; anything not listed falls through to muted.
const statusTonesByName: Record<string, StatusBadgeTone> = {
  active: 'success',
  running: 'success',
  expired: 'destructive',
  not_configured: 'destructive',
  starting: 'in-progress',
  stopping: 'in-progress',
  pending: 'in-progress',
  stopped: 'muted',
  unknown: 'warning',
  '': 'warning',
};

export function cloudProviderStatusTone(status: string): StatusBadgeTone {
  return statusTonesByName[status.trim()] ?? 'muted';
}

export type StatusBadgeTone = 'success' | 'warning' | 'destructive' | 'in-progress' | 'muted';

// Lookup table keeps cloudProviderStatusTone within the eslint complexity
// ceiling. Keys are the canonical cloud-context status strings emitted by
// the backend.
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

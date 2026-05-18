export type StatusBadgeTone = 'success' | 'destructive' | 'muted';

export function cloudProviderStatusTone(status: string): StatusBadgeTone {
  const normalized = status.trim();
  if (normalized === 'active' || normalized === 'running') {
    return 'success';
  }
  if (normalized === 'expired' || normalized === 'not_configured') {
    return 'destructive';
  }
  return 'muted';
}

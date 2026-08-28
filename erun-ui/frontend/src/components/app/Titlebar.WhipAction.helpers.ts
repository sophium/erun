import type { StatusBadgeTone } from 'erun-kit';

// whipOutcomeTone maps a whip result's outcome to the shared StatusBadge tone
// vocabulary (erun-ui/AGENTS.md's Design-Language Decision Record, "One
// status-badge component"), following the same named-helper-beside-the-domain-
// type precedent as reviewStatusTone/cloudProviderStatusTone.
const whipOutcomeTones: Record<string, StatusBadgeTone> = {
  pushed: 'success',
  capped: 'warning',
  failed: 'destructive',
  skipped: 'muted',
};

export function whipOutcomeTone(outcome: string): StatusBadgeTone {
  return whipOutcomeTones[outcome] ?? 'muted';
}

export function whipOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'pushed':
      return 'Pushed';
    case 'capped':
      return 'Capped';
    case 'failed':
      return 'Failed';
    default:
      return 'Skipped';
  }
}

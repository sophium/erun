import type { StatusBadgeTone } from 'erun-kit';

import type { orchestrator } from '../../../wailsjs/go/models';

// whipReportAllSucceeded gates auto-dismiss; an empty result list is not success.
export function whipReportAllSucceeded(
  report: orchestrator.WhipReport | null | undefined,
): boolean {
  const results = report?.results ?? [];
  return (
    results.length > 0 && results.every((result) => whipOutcomeTone(result.outcome) === 'success')
  );
}

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

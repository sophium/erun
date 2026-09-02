import type { StatusBadgeTone } from 'erun-kit';

import type { main } from '../../../wailsjs/go/models';

// whipReportAllSucceeded reports whether every targeted result in a whip
// report came back 'pushed' (whipOutcomeTone's only 'success' outcome) --
// the auto-dismiss gate. An empty result list ("nothing was targeted") is
// deliberately not "all succeeded": nothing happened, which is exactly the
// case the operator needs to read, not have vanish on them.
export function whipReportAllSucceeded(report: main.uiWhipReport | null | undefined): boolean {
  const results = report?.results ?? [];
  return (
    results.length > 0 && results.every((result) => whipOutcomeTone(result.outcome) === 'success')
  );
}

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

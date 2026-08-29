import type { StatusBadgeTone } from 'erun-kit';

import type { UIDiffReviewStatus } from '@/types';

// diffReviewChipTone/diffReviewChipLabel map the diff panel's review-status
// chip to StatusBadge's shared tone vocabulary and a user-facing label,
// mirroring reviewStatusTone/unresolvedThreadsLabel in tenantDashboardPanels.ts
// (the platform review status these chip states are ultimately derived from).
export function diffReviewChipTone(state: string): StatusBadgeTone {
  switch (state) {
    case 'ready':
    case 'merged':
      return 'success';
    case 'blocked':
      return 'warning';
    case 'failed':
      return 'destructive';
    case 'merging':
    case 'checking':
      return 'in-progress';
    default:
      return 'muted';
  }
}

// diffReviewChipFixedLabels covers every chip state whose label needs no
// data from the rest of the status -- 'ready' and 'blocked' are handled
// separately in diffReviewChipLabel since their label depends on
// queuePosition/unresolvedThreads.
const diffReviewChipFixedLabels: Record<string, string> = {
  checking: 'Checking status…',
  unavailable: 'Status unknown',
  none: 'No review',
  open: 'Open · building',
  failed: 'Build failed',
  merging: 'Merging',
  merged: 'Merged',
  closed: 'Closed',
};

export function diffReviewChipLabel(status: UIDiffReviewStatus): string {
  if (status.state === 'ready') {
    return status.queuePosition ? `Ready · queued #${String(status.queuePosition)}` : 'Ready';
  }
  if (status.state === 'blocked') {
    return `Blocked · ${diffReviewThreadCount(status)}`;
  }
  return diffReviewChipFixedLabels[status.state] ?? 'Status unknown';
}

// diffReviewThreadCount renders the count as a quantity, matching
// unresolvedThreadsLabel's own "N unresolved" / "1 unresolved" shape rather
// than a bare number the chip's short label would otherwise leave ambiguous.
export function diffReviewThreadCount(status: UIDiffReviewStatus): string {
  const count = status.unresolvedThreads ?? 0;
  return count === 1 ? '1 thread' : `${String(count)} threads`;
}

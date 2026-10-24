import type { StatusBadgeTone } from 'erun-kit';

import type { UIDiffReviewStatus } from '@/types';

// Mirrors reviewStatusTone/unresolvedThreadsLabel in tenantDashboardPanels.ts.
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

// 'ready' and 'blocked' are labelled in diffReviewChipLabel from status data.
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

export function diffReviewThreadCount(status: UIDiffReviewStatus): string {
  const count = status.unresolvedThreads ?? 0;
  return count === 1 ? '1 thread' : `${String(count)} threads`;
}

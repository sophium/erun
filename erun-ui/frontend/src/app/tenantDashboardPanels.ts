import type { StatusBadgeTone } from 'erun-kit';

import type { UITenant, UITenantDashboard, UITenantDashboardPanel } from '@/types';

import type { TenantDashboardTab } from './state';

// tenantDashboardEnvironmentName picks the env the dashboard loaded against,
// else the first with an apiUrl (default first). Here to avoid a component import cycle.
export function tenantDashboardEnvironmentName(
  tenant: UITenant | undefined,
  loadedEnvironment: string | undefined,
): string {
  const environmentName = loadedEnvironment?.trim();
  if (environmentName) {
    return environmentName;
  }
  if (!tenant) {
    return '';
  }
  const defaultEnvironment = tenant.defaultEnvironment?.trim();
  const environment =
    tenant.environments.find(
      (candidate) => candidate.name === defaultEnvironment && candidate.apiUrl,
    ) ?? tenant.environments.find((candidate) => candidate.apiUrl);
  return environment?.name.trim() ?? '';
}

// tenantDashboardPlatformTenantName is whoami's platform tenant name, shown
// even when it matches the local name so absence means "not reported".
export function tenantDashboardPlatformTenantName(
  data: UITenantDashboard | null | undefined,
): string {
  return data?.user?.tenantName?.trim() ?? '';
}

export interface TenantDashboardTabDescriptor {
  tab: TenantDashboardTab;
  label: string;
}

// The API log is read over the MCP edge, so it carries no permission of its own.
export const tenantDashboardTabs: readonly TenantDashboardTabDescriptor[] = [
  { tab: 'users', label: 'Users' },
  { tab: 'reviews', label: 'Reviews' },
  { tab: 'queue', label: 'Merge queue' },
  { tab: 'pipeline', label: 'Pipeline' },
  { tab: 'builds', label: 'Builds' },
  { tab: 'audit', label: 'Audit log' },
  { tab: 'registration', label: 'Registration' },
  { tab: 'requests', label: 'Requests' },
  { tab: 'api-log', label: 'API log' },
];

export function tenantDashboardPanel(
  data: UITenantDashboard | null | undefined,
  tab: TenantDashboardTab,
): UITenantDashboardPanel | undefined {
  return data?.panels?.find((panel) => panel.tab === tab);
}

// visibleTenantDashboardTabs keeps every tab until permissions are known.
export function visibleTenantDashboardTabs(
  data: UITenantDashboard | null | undefined,
): TenantDashboardTabDescriptor[] {
  return tenantDashboardTabs.filter((descriptor) => {
    const restricted = tenantDashboardPanel(data, descriptor.tab)?.restricted;
    return !restricted;
  });
}

export function restrictedTenantDashboardReads(
  data: UITenantDashboard | null | undefined,
): string[] {
  const reads = new Set<string>();
  for (const descriptor of tenantDashboardTabs) {
    const restricted = tenantDashboardPanel(data, descriptor.tab)?.restricted;
    if (restricted) {
      reads.add(restricted);
    }
  }
  return [...reads];
}

export function activeTenantDashboardTab(
  data: UITenantDashboard | null | undefined,
  selected: TenantDashboardTab,
): TenantDashboardTab {
  const visible = visibleTenantDashboardTabs(data);
  if (visible.some((descriptor) => descriptor.tab === selected)) {
    return selected;
  }
  return visible[0]?.tab ?? selected;
}

// mergeQueueAddress returns the single queue a panel may advance, or none when
// it spans branches or repositories; an all-empty repository is one queue.
export function mergeQueueAddress(
  mergeQueue: readonly { repository?: string; targetBranch: string }[],
): { repository: string; targetBranch: string } | null {
  const repositories = [...new Set(mergeQueue.map((review) => (review.repository ?? '').trim()))];
  const branches = [...new Set(mergeQueue.map((review) => review.targetBranch.trim()))];
  if (repositories.length !== 1 || branches.length !== 1) {
    return null;
  }
  const repository = repositories[0] ?? '';
  const targetBranch = branches[0] ?? '';
  if (!targetBranch) {
    return null;
  }
  return { repository, targetBranch };
}

// A queue with no repository is named by its branch alone.
export function mergeQueueHeadLabel(queue: { repository: string; targetBranch: string }): string {
  return queue.repository
    ? `${queue.repository}'s head into ${queue.targetBranch}`
    : queue.targetBranch;
}

// Every tone keeps the status word as its label (WCAG 1.4.1).
const reviewStatusTones: Record<string, StatusBadgeTone> = {
  OPEN: 'muted',
  READY: 'success',
  MERGE: 'in-progress',
  MERGED: 'success',
  FAILED: 'destructive',
  CLOSED: 'muted',
};

export function reviewStatusTone(status: string): StatusBadgeTone {
  return reviewStatusTones[status.trim().toUpperCase()] ?? 'warning';
}

// Hosted environments and cloud contexts share one status vocabulary.
const registrationStatusTones: Record<string, StatusBadgeTone> = {
  registered: 'muted',
  provisioning: 'in-progress',
  running: 'success',
  failed: 'destructive',
  deleting: 'in-progress',
  'deletion-blocked': 'destructive',
};

export function registrationStatusTone(status: string): StatusBadgeTone {
  return registrationStatusTones[status.trim().toLowerCase()] ?? 'warning';
}

const inviteRequestStatusTones: Record<string, StatusBadgeTone> = {
  PENDING: 'in-progress',
  APPROVED: 'success',
  DECLINED: 'destructive',
};

export function inviteRequestStatusTone(status: string): StatusBadgeTone {
  return inviteRequestStatusTones[status.trim().toUpperCase()] ?? 'muted';
}

// The pending count is shown on the tab so an unattended queue is visible.
export function requestsTabLabel(data: UITenantDashboard | null | undefined): string {
  const count = data?.pendingInviteRequestCount;
  return count ? `Requests (${String(count)})` : 'Requests';
}

export function unresolvedThreadsTone(count: number): StatusBadgeTone {
  return count === 0 ? 'success' : 'warning';
}

export function unresolvedThreadsLabel(count: number): string {
  if (count === 0) {
    return 'All resolved';
  }
  return count === 1 ? '1 unresolved' : `${String(count)} unresolved`;
}

// A thread's status lives on its root; replies carry none.
export interface ThreadRootComment {
  parentCommentId?: string;
  status?: string;
}

// unresolvedThreadCount mirrors erun-common's CountUnresolvedThreads; prefer it
// over a summary field so the list and the detail dialog agree.
export function unresolvedThreadCount(comments: readonly ThreadRootComment[] | undefined): number {
  return (comments ?? []).filter(
    (comment) => !comment.parentCommentId?.trim() && comment.status === 'OPEN',
  ).length;
}

// reviewRowUnresolvedThreads is undefined when the Go enrichment could not compute it.
export function reviewRowUnresolvedThreads(row: {
  unresolvedThreads?: number;
}): number | undefined {
  return row.unresolvedThreads;
}

// reviewDetailUnresolvedThreads derives from loaded comments, else the summary,
// and never guesses zero.
export function reviewDetailUnresolvedThreads(detail: {
  comments?: readonly ThreadRootComment[];
  unresolvedThreads?: number;
}): number | undefined {
  if (detail.comments) {
    return unresolvedThreadCount(detail.comments);
  }
  return detail.unresolvedThreads;
}

// Unknown is spelled out, since a dash reads as "none".
export function unresolvedThreadsCountLabel(count: number | undefined): string {
  return count === undefined ? 'Unknown' : unresolvedThreadsLabel(count);
}

// formatDashboardDate falls back to the raw value for unrecognised shapes.
export function formatDashboardDate(value: string | undefined): string {
  if (!value) {
    return '-';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

const relativeTimeFormatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

const relativeTimeUnits: { unit: Intl.RelativeTimeFormatUnit; ms: number }[] = [
  { unit: 'year', ms: 365 * 24 * 60 * 60 * 1000 },
  { unit: 'month', ms: 30 * 24 * 60 * 60 * 1000 },
  { unit: 'day', ms: 24 * 60 * 60 * 1000 },
  { unit: 'hour', ms: 60 * 60 * 1000 },
  { unit: 'minute', ms: 60 * 1000 },
];

export function relativeDashboardDate(value: string | undefined): string {
  if (!value) {
    return '-';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const diffMs = date.getTime() - Date.now();
  for (const { unit, ms } of relativeTimeUnits) {
    if (Math.abs(diffMs) >= ms) {
      return relativeTimeFormatter.format(Math.round(diffMs / ms), unit);
    }
  }
  return relativeTimeFormatter.format(Math.round(diffMs / 1000), 'second');
}

export function reviewAuthorInitials(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) {
    return '?';
  }
  if (trimmed === 'You') {
    return 'Y';
  }
  const tokens = trimmed.split(/[\s._-]+/).filter(Boolean);
  const first = tokens[0]?.[0] ?? '';
  const second = tokens[1]?.[0] ?? tokens[0]?.[1] ?? '';
  const initials = (first + second).toUpperCase();
  return initials || '?';
}

export function middleEllipsis(value: string, keep = 20): string {
  const trimmed = value.trim();
  if (trimmed.length <= keep * 2 + 1) {
    return trimmed;
  }
  return `${trimmed.slice(0, keep)}…${trimmed.slice(-keep)}`;
}

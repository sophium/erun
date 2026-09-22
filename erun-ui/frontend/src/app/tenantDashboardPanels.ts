import type { StatusBadgeTone } from 'erun-kit';

import type { UITenant, UITenantDashboard, UITenantDashboardPanel } from '@/types';

import type { TenantDashboardTab } from './state';

// tenantDashboardEnvironmentName resolves which of tenant's local
// environments the dashboard header (and the request-invitation dialog's
// prefill) names: the environment the dashboard actually loaded against when
// set, else the first one with a resolvable apiUrl (preferring the tenant's
// own default). Lives here (not in a component file) so both
// TenantDashboardView.tsx and TenantPlatformState.tsx can import it without
// creating a circular dependency between the two.
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

export interface TenantDashboardTabDescriptor {
  tab: TenantDashboardTab;
  label: string;
}

// The API log is read over the environment's MCP edge rather than the platform
// API, so it carries no permission of its own.
export const tenantDashboardTabs: readonly TenantDashboardTabDescriptor[] = [
  { tab: 'users', label: 'Users' },
  { tab: 'reviews', label: 'Reviews' },
  { tab: 'queue', label: 'Merge queue' },
  { tab: 'gates', label: 'Gates' },
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

// visibleTenantDashboardTabs drops the tabs the signed-in user may not open. A
// dashboard that reported no panels at all has not answered yet, so every tab
// stays — an unknown permission is not a denied one.
export function visibleTenantDashboardTabs(
  data: UITenantDashboard | null | undefined,
): TenantDashboardTabDescriptor[] {
  return tenantDashboardTabs.filter((descriptor) => {
    const restricted = tenantDashboardPanel(data, descriptor.tab)?.restricted;
    return !restricted;
  });
}

// restrictedTenantDashboardReads names the access the signed-in user is missing,
// so the reason a tab is absent can be shown rather than guessed.
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

// activeTenantDashboardTab keeps the selected tab on one the user can open, so a
// restricted tab never renders as a blank panel.
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

// mergeQueueAddress reports the one queue a panel's "Advance queue" action
// may act on, only when the whole visible queue names one. Advancing is a
// single-queue-head write, so a panel spanning branches, or repositories, has
// no single unambiguous head to name.
//
// A queue whose reviews all record no repository — what a tenant whose reviews
// all predate repository identity looks like — still has one address, with an
// empty repository: every one of those reviews is in the same one queue, and
// the platform promotes it exactly as it always did. Only a genuine mixture is
// refused here, and the platform refuses the same mixture again if this ever
// let one through.
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

// mergeQueueHeadLabel names the head an "Advance queue" confirm is about. A
// queue whose reviews record no repository is named by its branch alone rather
// than by an empty string, which would read as a missing value.
export function mergeQueueHeadLabel(queue: { repository: string; targetBranch: string }): string {
  return queue.repository
    ? `${queue.repository}'s head into ${queue.targetBranch}`
    : queue.targetBranch;
}

// reviewStatusTones maps the collaboration API's review status vocabulary to
// a StatusBadge tone. WCAG 1.4.1 requires status not be conveyed by colour
// alone, so every tone still carries the status word as its label.
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

// gateRunStatusTones maps a gate run's verdict vocabulary to a StatusBadge
// tone. The one thing this must get right (erun#1932): INCONCLUSIVE is not a
// failure -- it exists precisely because a wrapper hitting its own timeout,
// or an environment fault, is not a verdict on the change. It renders with
// its own `warning` tone, visibly distinct from FAILED's `destructive` tone,
// never folded into the same red state (see erun-backend-api/AGENTS.md's
// "Gate Runs"). WCAG 1.4.1: every tone still shows the status word.
const gateRunStatusTones: Record<string, StatusBadgeTone> = {
  RUNNING: 'in-progress',
  PASSED: 'success',
  FAILED: 'destructive',
  INCONCLUSIVE: 'warning',
};

export function gateRunStatusTone(status: string): StatusBadgeTone {
  return gateRunStatusTones[status.trim().toUpperCase()] ?? 'warning';
}

// registrationStatusTones maps the Registration tab's two status vocabularies
// (hosted environments and cloud contexts share the same
// registered/provisioning/running/failed/deleting/deletion-blocked shape) to
// a StatusBadge tone. WCAG 1.4.1 again: every tone still shows the status
// word, never colour alone.
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

// inviteRequestStatusTones maps the invite-request queue's status vocabulary
// (PENDING/APPROVED/DECLINED) to a StatusBadge tone. WCAG 1.4.1: every tone
// still shows the status word, never colour alone.
const inviteRequestStatusTones: Record<string, StatusBadgeTone> = {
  PENDING: 'in-progress',
  APPROVED: 'success',
  DECLINED: 'destructive',
};

export function inviteRequestStatusTone(status: string): StatusBadgeTone {
  return inviteRequestStatusTones[status.trim().toUpperCase()] ?? 'muted';
}

// requestsTabLabel is the tab strip's own label for the Requests tab: the
// pending count is visible before the operator opens the panel at all — an
// unattended queue nobody sees is the failure this exists to prevent.
export function requestsTabLabel(data: UITenantDashboard | null | undefined): string {
  const count = data?.pendingInviteRequestCount;
  return count ? `Requests (${String(count)})` : 'Requests';
}

// unresolvedThreadsTone renders the count as a quantity, not just a colour:
// zero reads as done (success), any other count as still-open (warning) —
// WCAG 1.4.1 again, so the label carries the fact even without colour.
export function unresolvedThreadsTone(count: number): StatusBadgeTone {
  return count === 0 ? 'success' : 'warning';
}

export function unresolvedThreadsLabel(count: number): string {
  if (count === 0) {
    return 'All resolved';
  }
  return count === 1 ? '1 unresolved' : `${String(count)} unresolved`;
}

// threadRootComment is the minimal shape unresolvedThreadCount needs: a
// thread's status lives entirely on its root, and replies never carry their
// own, which is the same rule erun-common's CountUnresolvedThreads applies.
export interface ThreadRootComment {
  parentCommentId?: string;
  status?: string;
}

// unresolvedThreadCount derives the count from a review's own comment list,
// mirroring erun-common's CountUnresolvedThreads. Surfaces that have the
// threads must derive from them rather than from a summary field: the two
// producers of this number (the dashboard row's enrichment and the detail
// dialog's comment read) could otherwise disagree about the same review, and
// a list row reading "-" beside a dialog reading "1 unresolved" is exactly
// that disagreement.
export function unresolvedThreadCount(comments: readonly ThreadRootComment[] | undefined): number {
  return (comments ?? []).filter(
    (comment) => !comment.parentCommentId?.trim() && comment.status === 'OPEN',
  ).length;
}

// reviewRowUnresolvedThreads is the reviews list's read of a row's
// unresolved-thread count: the Go dashboard's own per-review enrichment, or
// undefined when that enrichment could not compute it for this row.
export function reviewRowUnresolvedThreads(row: {
  unresolvedThreads?: number;
}): number | undefined {
  return row.unresolvedThreads;
}

// reviewDetailUnresolvedThreads is the review detail dialog's read of the
// same quantity. The dialog holds the review's comment threads, so it derives
// the count from them — the same derivation erun-common's
// CountUnresolvedThreads applies — instead of trusting a summary field that
// can drift from the list's. It falls back to the summary only when no
// comment list was loaded at all, and never guesses zero: a review whose
// count is unknown is not a review with nothing left open.
export function reviewDetailUnresolvedThreads(detail: {
  comments?: readonly ThreadRootComment[];
  unresolvedThreads?: number;
}): number | undefined {
  if (detail.comments) {
    return unresolvedThreadCount(detail.comments);
  }
  return detail.unresolvedThreads;
}

// unresolvedThreadsCountLabel renders either surface's count. Unknown is
// spelled out rather than shown as a bare dash, because a dash beside a
// dialog that reports a count is indistinguishable from "none".
export function unresolvedThreadsCountLabel(count: number | undefined): string {
  return count === undefined ? 'Unknown' : unresolvedThreadsLabel(count);
}

// formatDashboardDate renders a timestamp in the operator's own locale, and
// falls back to the raw value rather than hiding one the API sent in a shape
// this build does not recognise. Used as the absolute value behind
// relativeDashboardDate's hover/title, and directly wherever a full
// timestamp (not a scannable relative one) is what's needed.
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

// relativeDashboardDate renders "2 days ago" instead of a raw locale string —
// nobody scans absolute timestamps in a column of rows (#1378). Callers pair
// this with formatDashboardDate as the hover/title value so the exact moment
// is still one hover away, never lost.
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

// reviewAuthorInitials derives an avatar's letters from a display name (a
// resolved username, "You", or a raw id fallback) — up to two characters, so
// the avatar is still a meaningful scan key when no username resolved.
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

// middleEllipsis keeps both ends of an identifier visible — a branch name's
// prefix and suffix both carry meaning, unlike prose, where a trailing "…"
// would drop the more identifying half (#1378).
export function middleEllipsis(value: string, keep = 20): string {
  const trimmed = value.trim();
  if (trimmed.length <= keep * 2 + 1) {
    return trimmed;
  }
  return `${trimmed.slice(0, keep)}…${trimmed.slice(-keep)}`;
}

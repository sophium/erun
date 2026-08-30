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
// pending count is visible before the operator opens the panel at all (issue
// #1682 §3 — "an unattended queue nobody sees" is the failure this exists to
// prevent).
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

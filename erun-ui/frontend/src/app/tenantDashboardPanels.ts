import type { StatusBadgeTone } from 'erun-kit';

import type { UITenantDashboard, UITenantDashboardPanel } from '@/types';

import type { TenantDashboardTab } from './state';

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
// this build does not recognise.
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

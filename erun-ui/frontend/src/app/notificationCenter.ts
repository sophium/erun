import type { StatusBadgeTone } from 'erun-kit';

import type { AppNotification, AppNotificationKind } from './state';

// 'info' and 'debug' share a tone; the label tells them apart.
const notificationKindTones: Record<AppNotificationKind, StatusBadgeTone> = {
  success: 'success',
  warning: 'warning',
  error: 'destructive',
  info: 'muted',
  debug: 'muted',
};

export function notificationKindTone(kind: AppNotificationKind): StatusBadgeTone {
  return notificationKindTones[kind];
}

const notificationKindLabels: Record<AppNotificationKind, string> = {
  success: 'Success',
  warning: 'Warning',
  error: 'Error',
  info: 'Info',
  debug: 'Debug',
};

export function notificationKindLabel(kind: AppNotificationKind): string {
  return notificationKindLabels[kind];
}

// Only 'debug' gets no titlebar icon; 'success' keeps one because it is the only
// signal a one-shot success gets.
export const TITLEBAR_ICON_KINDS: readonly AppNotificationKind[] = [
  'error',
  'warning',
  'info',
  'success',
];

export const DIALOG_FILTER_KINDS: readonly AppNotificationKind[] = [
  'error',
  'warning',
  'info',
  'success',
  'debug',
];

export type NotificationCounts = Record<AppNotificationKind, number>;

function emptyCounts(): NotificationCounts {
  return { error: 0, warning: 0, info: 0, success: 0, debug: 0 };
}

export function unreadNotificationCounts(notifications: AppNotification[]): NotificationCounts {
  const counts = emptyCounts();
  for (const n of notifications) {
    if (!n.dismissed) {
      counts[n.kind] += 1;
    }
  }
  return counts;
}

export function totalUnreadCount(counts: NotificationCounts): number {
  return Object.values(counts).reduce((sum, count) => sum + count, 0);
}

export function notificationHistoryNewestFirst(
  notifications: AppNotification[],
): AppNotification[] {
  return [...notifications].sort((a, b) => b.timestamp - a.timestamp);
}

// notificationIdentityLabel is null for an untagged notification.
export function notificationIdentityLabel(n: AppNotification): string | null {
  if (n.tenant && n.environment) {
    return `${n.tenant} / ${n.environment}`;
  }
  if (n.orchestratorId) {
    return `Orchestrator ${n.orchestratorId}`;
  }
  return null;
}

export type NotificationFilter = 'all' | AppNotificationKind;

// filterNotificationHistory hides 'debug' from every filter unless showDebug.
export function filterNotificationHistory(
  notifications: AppNotification[],
  filter: NotificationFilter,
  showDebug: boolean,
): AppNotification[] {
  return notifications.filter((n) => {
    if (n.kind === 'debug' && !showDebug) {
      return false;
    }
    return filter === 'all' || n.kind === filter;
  });
}

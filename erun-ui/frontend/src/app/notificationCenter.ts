// notificationCenter holds the classification/derivation logic for the
// titlebar's message centre: which classes stand as their own
// titlebar icon, how many unread entries each class has, and how the message
// centre dialog orders and filters the session's full history. Kept apart
// from notificationSlice (state shape) and Titlebar.Status.tsx (rendering) so
// the classification rules are testable without mounting a component.
import type { StatusBadgeTone } from 'erun-kit';

import type { AppNotification, AppNotificationKind } from './state';

// notificationKindTone maps a message class onto the shared StatusBadge tone
// vocabulary (erun-ui/AGENTS.md's Design-Language Decision Record, "One
// status-badge component"), following the same named-helper-beside-the-
// domain-type precedent as whipOutcomeTone/reviewStatusTone. 'info' and
// 'debug' share 'muted' -- the label text (notificationKindLabel) is what
// tells them apart, the same way StatusBadge tones are shared across
// multiple domain outcomes elsewhere.
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

// TITLEBAR_ICON_KINDS excludes only 'debug': that class never gets a titlebar
// icon at all, since it is diagnostic detail the operator opts into from
// inside the dialog, never surfaced ambiently ("hidden by default, revealed
// by a toggle in the dialog"). 'success' DOES get one
// despite auto-dismissing quickly -- a one-shot confirmation with literally
// no visible affordance (not even a flash) is a worse regression than the
// icon reading "0" a few seconds later: it is the only signal a "Created
// tenant/env" or similar success ever gets, so dropping it silently would
// fail Nielsen #1 (visibility of system status) outright.
export const TITLEBAR_ICON_KINDS: readonly AppNotificationKind[] = [
  'error',
  'warning',
  'info',
  'success',
];

// DIALOG_FILTER_KINDS is every class the message centre dialog can filter to,
// in display order. 'debug' is included here (not in TITLEBAR_ICON_KINDS)
// because the dialog is exactly where the "reveal debug" toggle lives.
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

// unreadNotificationCounts counts not-yet-dismissed entries per kind, for the
// titlebar icon badges. A dismissed entry (auto-dismissed or acknowledged)
// stops counting here but stays in history for the dialog.
export function unreadNotificationCounts(notifications: AppNotification[]): NotificationCounts {
  const counts = emptyCounts();
  for (const n of notifications) {
    if (!n.dismissed) {
      counts[n.kind] += 1;
    }
  }
  return counts;
}

// notificationHistoryNewestFirst is the message centre dialog's own order --
// most recent first, since that is what an operator checking back on a
// session's messages wants to see at the top.
export function notificationHistoryNewestFirst(
  notifications: AppNotification[],
): AppNotification[] {
  return [...notifications].sort((a, b) => b.timestamp - a.timestamp);
}

// notificationIdentityLabel names which env or orchestrator a message
// describes, so the dialog never shows an anonymous line for a tagged
// notification. Returns null for a message with no identity to show
// (a one-shot toast with no tenant/environment/orchestratorId).
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

// filterNotificationHistory applies the dialog's class filter plus the debug
// visibility toggle. 'debug' entries are excluded from every filter
// (including 'all') unless showDebug is true, and from the 'debug' filter
// itself when off -- so turning the toggle off always hides them, regardless
// of which tab was selected.
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

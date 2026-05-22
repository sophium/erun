import type { AppNotification } from '../state';

// AppNotificationPayload carries a transient toast-style notification
// emitted by the Go side (see erun-ui/app.go appNotificationEvent and
// emitAppNotification). The frontend routes it through the auto-
// dismissing notification slot rather than the titlebar terminal-status
// pill so one-shot info/success events cannot go stale. See issue #361.
export interface AppNotificationPayload {
  kind?: AppNotification['kind'];
  message?: string;
}

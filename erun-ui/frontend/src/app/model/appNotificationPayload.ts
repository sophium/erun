import type { AppNotification } from '../state';

// AppNotificationPayload carries a transient toast-style notification
// emitted by the Go side (see erun-ui/app.go appNotificationEvent and
// emitAppNotification). The frontend routes it through the auto-
// dismissing notification slot rather than the titlebar terminal-status
// pill so one-shot info/success events cannot go stale.
export interface AppNotificationPayload {
  kind?: AppNotification['kind'];
  message?: string;
  // Env tag: the runtime-unreachable warning carries the env it
  // targets and a stable source so the deploy lifecycle can clear it later.
  tenant?: string;
  environment?: string;
  source?: string;
}

// AppNotificationClearPayload dismisses an env-tagged notification (see the Go
// appNotificationClearEvent). The frontend clears the current notification only
// when its source/tenant/environment all match.
export interface AppNotificationClearPayload {
  tenant?: string;
  environment?: string;
  source?: string;
}

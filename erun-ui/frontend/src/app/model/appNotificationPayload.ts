import type { AppNotification } from '../state';

// AppNotificationPayload carries a transient notification routed through the
// auto-dismissing slot (not the titlebar status pill) so one-shot info/success
// events cannot go stale.
export interface AppNotificationPayload {
  kind?: AppNotification['kind'];
  message?: string;
  // Env tag so the deploy lifecycle can later clear the notification it targeted.
  tenant?: string;
  environment?: string;
  source?: string;
  // Action names a control the titlebar can render that performs the
  // message's own remedy directly, e.g. "deploy" opens the tagged env's
  // deploy dialog. Undefined means the message carries no action (#1390).
  action?: AppNotification['action'];
}

// AppNotificationClearPayload dismisses an env-tagged notification only when all
// tag fields match, so a clear cannot dismiss an unrelated notification.
export interface AppNotificationClearPayload {
  tenant?: string;
  environment?: string;
  source?: string;
}

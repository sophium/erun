import type { AppNotification } from '../state';

// AppNotificationPayload goes through the auto-dismissing slot so one-shot events
// cannot go stale.
export interface AppNotificationPayload {
  kind?: AppNotification['kind'];
  message?: string;
  tenant?: string;
  environment?: string;
  source?: string;
  orchestratorId?: string;
  action?: AppNotification['action'];
}

// AppNotificationClearPayload dismisses only when every tag field matches.
export interface AppNotificationClearPayload {
  tenant?: string;
  environment?: string;
  source?: string;
}

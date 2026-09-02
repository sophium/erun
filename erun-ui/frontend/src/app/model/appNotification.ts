import type { AppNotificationAction } from './appNotificationAction';

// AppNotificationKind is the message centre's full taxonomy.
// 'debug' is diagnostic detail: never rendered as a titlebar icon, and hidden
// in the message centre dialog until the operator toggles it on -- keeping it
// out of the way is the point, not an oversight.
export type AppNotificationKind = 'success' | 'warning' | 'error' | 'info' | 'debug';

export interface AppNotification {
  // Unique per queued entry so a specific one can be dismissed (auto-dismiss
  // timer, explicit dismiss click) without disturbing sibling entries queued
  // before or after it.
  id: string;
  kind: AppNotificationKind;
  message: string;
  // Epoch ms when the notification was raised, shown in the message centre
  // dialog so a history entry can be placed in time.
  timestamp: number;
  // Dismissing a notification (auto-dismiss, explicit click, or an env-scoped
  // clear) never deletes it -- it only marks it read. The message centre
  // dialog is the durable, session-scoped history this retains it for; only
  // the titlebar's unread counts (and the old single-pill display) care about
  // this flag.
  dismissed: boolean;
  // Optional tags so a notification can be dismissed later by the state that raised it.
  tenant?: string;
  environment?: string;
  source?: string;
  // orchestratorId is the orchestrator-scoped analogue of tenant/environment,
  // set when the notification's action operates on a specific orchestrator
  // (e.g. restarting it) rather than a specific env.
  orchestratorId?: string;
  // Action names a control TitlebarStatus can render beside the message that
  // performs the message's own remedy directly -- see AppNotificationAction.
  // Undefined means no action.
  action?: AppNotificationAction;
}

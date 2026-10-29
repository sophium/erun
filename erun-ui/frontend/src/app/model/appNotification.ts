import type { AppNotificationAction } from './appNotificationAction';

// AppNotificationKind 'debug' never shows in the titlebar and is hidden in the
// message centre by default.
export type AppNotificationKind = 'success' | 'warning' | 'error' | 'info' | 'debug';

export interface AppNotification {
  id: string;
  kind: AppNotificationKind;
  message: string;
  // Epoch ms.
  timestamp: number;
  // Dismissal only marks read; the message centre keeps the history.
  dismissed: boolean;
  // Tags so the raising state can dismiss it later.
  tenant?: string;
  environment?: string;
  source?: string;
  orchestratorId?: string;
  action?: AppNotificationAction;
}

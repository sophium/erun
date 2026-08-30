// AppNotificationAction is AppNotification['action']'s own union, split out
// of state.ts (already at eslint's max-lines budget for that file) so a new
// notification remedy can be added without growing it further.
export type AppNotificationAction =
  | 'deploy'
  | 'restart-orchestrator'
  | 'install-and-restart-orchestrator'
  | 'invite-approved';

// Shared copy for the runtime-reconnect flow, reused across the review panel
// and manage dialog so recovery reads as one consistent action.
//
// The marker mirrors the opaque prefix the Go backend puts on MCP-unreachable
// errors; it is a machine token and must never be shown to users.
export const MCP_UNREACHABLE_MARKER = 'ERUN_MCP_UNREACHABLE: ';

export const reconnectCopy = {
  errorTitle: 'Cannot reach the environment runtime',
  errorBody: 'The diff could not be loaded because the connection to your environment is down.',
  retryAction: 'Retry',
  reconnectAction: 'Reconnect…',
  dialogTitle: 'Reconnect to environment?',
  dialogBody:
    'This runs `erun open` to restore the connection. If the environment runtime is not currently running, it will be redeployed.',
  dialogCancel: 'Cancel',
  dialogConfirm: 'Reconnect',
  runningStatus: 'Reconnecting…',
  runningHint: 'Latest output will appear below.',
  errorStatusTitle: 'Reconnect failed',
  retry: 'Retry',
  dismiss: 'Dismiss',
} as const;

export function stripMcpUnreachableMarker(message: string): string {
  if (message.startsWith(MCP_UNREACHABLE_MARKER)) {
    return message.slice(MCP_UNREACHABLE_MARKER.length);
  }
  return message;
}

export function isMcpUnreachableMessage(message: string): boolean {
  return message.startsWith(MCP_UNREACHABLE_MARKER);
}

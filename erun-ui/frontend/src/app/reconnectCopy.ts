// Strings shown when the desktop loses its connection to an environment's
// in-cluster runtime (the local MCP port-forward is unreachable). The same
// copy is reused across surfaces — review panel, manage dialog — so users
// see consistent labels for what is essentially one recovery flow.
//
// The marker comes from erun-ui/mcp_errors.go and is opaque; it never
// appears in user-facing text.
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

// Strip the opaque marker from a wrapped backend error before surfacing it
// in user-facing text. The marker prefix indicates the typed error category
// but is never shown verbatim.
export function stripMcpUnreachableMarker(message: string): string {
  if (message.startsWith(MCP_UNREACHABLE_MARKER)) {
    return message.slice(MCP_UNREACHABLE_MARKER.length);
  }
  return message;
}

export function isMcpUnreachableMessage(message: string): boolean {
  return message.startsWith(MCP_UNREACHABLE_MARKER);
}

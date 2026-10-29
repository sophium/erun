// ReachabilityKind mirrors mcp.LocalMCPUnreachableKind: not-open is informational,
// stale-forward is a fault.
export type ReachabilityKind = 'not-open' | 'stale-forward';

// Mirrors mcpUnreachableKindMarkers in mcp_errors.go; never shown to users.
const MCP_UNREACHABLE_MARKERS: Record<ReachabilityKind, string> = {
  'not-open': 'ERUN_MCP_UNREACHABLE_NOT_OPEN: ',
  'stale-forward': 'ERUN_MCP_UNREACHABLE_STALE: ',
};

interface ReachabilityCopy {
  // Surface-neutral: shown verbatim by diff and jobs surfaces.
  errorTitle: string;
  errorBody: string;
  action: string;
  dialogTitle: string;
  dialogBody: string;
  dialogConfirm: string;
  runningStatus: string;
  errorStatusTitle: string;
}

// The dialog states what the action attempts, not achieves: `erun open
// --reconnect` never deploys or starts a stopped runtime.
const STALE_FORWARD_COPY: ReachabilityCopy = {
  errorTitle: 'Cannot reach the environment runtime',
  errorBody: 'The connection to your environment is down.',
  action: 'Reconnect…',
  dialogTitle: 'Reconnect to environment?',
  dialogBody:
    'This runs `erun open` to restore the connection. It reattaches to the environment runtime; it does not start a stopped environment or redeploy one.',
  dialogConfirm: 'Reconnect',
  runningStatus: 'Reconnecting…',
  errorStatusTitle: 'Reconnect failed',
};

// Not a fault, so the copy neither claims a lost connection nor promises a redeploy.
const NOT_OPEN_COPY: ReachabilityCopy = {
  errorTitle: 'Environment not running',
  errorBody: 'This environment is stopped — open it to continue.',
  action: 'Open',
  dialogTitle: 'Open environment?',
  dialogBody: 'This runs `erun open` to start the environment.',
  dialogConfirm: 'Open',
  runningStatus: 'Opening…',
  errorStatusTitle: 'Open failed',
};

export const reachabilityCopy: Record<ReachabilityKind, ReachabilityCopy> = {
  'stale-forward': STALE_FORWARD_COPY,
  'not-open': NOT_OPEN_COPY,
};

export const reconnectCopy = {
  retryAction: 'Retry',
  dialogCancel: 'Cancel',
  runningHint: 'Latest output will appear below.',
  retry: 'Retry',
  dismiss: 'Dismiss',
} as const;

export function stripMcpUnreachableMarker(message: string): string {
  const kind = mcpUnreachableKind(message);
  if (!kind) {
    return message;
  }
  return message.slice(MCP_UNREACHABLE_MARKERS[kind].length);
}

export function mcpUnreachableKind(message: string): ReachabilityKind | null {
  for (const kind of Object.keys(MCP_UNREACHABLE_MARKERS) as ReachabilityKind[]) {
    if (message.startsWith(MCP_UNREACHABLE_MARKERS[kind])) {
      return kind;
    }
  }
  return null;
}

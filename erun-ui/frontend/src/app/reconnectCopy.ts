// Shared copy for the runtime-reconnect flow, reused across the review panel
// and manage dialog so recovery reads as one consistent action.
//
// ReachabilityKind mirrors eruncommon.LocalMCPUnreachableKind: a stopped/
// never-opened environment (informational, "Open") is a different situation
// from a stale port-forward (a fault, "Reconnect…"), and the backend already
// tells the two apart -- the review panel used to flatten them into one
// "connection is down" error regardless (#1230).
export type ReachabilityKind = 'not-open' | 'stale-forward';

// The markers mirror the opaque prefixes the Go backend puts on
// MCP-unreachable errors (mcpUnreachableKindMarkers in erun-ui/mcp_errors.go);
// they are machine tokens and must never be shown to users.
const MCP_UNREACHABLE_MARKERS: Record<ReachabilityKind, string> = {
  'not-open': 'ERUN_MCP_UNREACHABLE_NOT_OPEN: ',
  'stale-forward': 'ERUN_MCP_UNREACHABLE_STALE: ',
};

interface ReachabilityCopy {
  // DiffErrorAlert / ChangedFilesAside / JobsTab status card. Kept
  // surface-neutral (no "diff", no "jobs") so it reads correctly wherever a
  // consumer shows it verbatim.
  errorTitle: string;
  errorBody: string;
  action: string;
  // ReconnectDialog confirmation.
  dialogTitle: string;
  dialogBody: string;
  dialogConfirm: string;
  // ReconnectStatusPanel while the action runs / after it fails.
  runningStatus: string;
  errorStatusTitle: string;
}

// staleForward is the genuine fault case: the port is held but the edge
// never answers. "Cannot reach the environment runtime" / "Reconnect…" holds
// regardless of which surface (diff, jobs) triggered the read.
//
// The dialog asks for confirmation and states what the action attempts, not
// what it will achieve. This dialog is reached *from* the unreachable state,
// so it is shown exactly when the environment may be down for a reason the
// reconnect cannot address -- and it is the moment an operator is most likely
// to press it repeatedly. It used to promise that a runtime which is not
// running "will be redeployed", which is not an unchecked recovery but one the
// action cannot perform at all: the reconnect runs `erun open --reconnect`,
// which verifies the runtime is already deployed, refuses outright when it is
// stopped, and never deploys. Saying so is what keeps the operator from
// reading a failed attempt as a "it just needs another try".
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

// notOpen is the ordinary resting state of an environment nobody has started
// (or that was stopped) -- not a fault, so the copy neither claims a
// connection existed to lose nor promises a redeploy for a runtime the local
// probe never actually checked (#1230).
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

// mcpUnreachableKind reports which reachability shape the message names, or
// null when the message is not one of the MCP-unreachable markers at all
// (an ordinary diff-loading error, unrelated to reachability).
export function mcpUnreachableKind(message: string): ReachabilityKind | null {
  for (const kind of Object.keys(MCP_UNREACHABLE_MARKERS) as ReachabilityKind[]) {
    if (message.startsWith(MCP_UNREACHABLE_MARKERS[kind])) {
      return kind;
    }
  }
  return null;
}

import { isRecord } from 'erun-kit';

// attachClient speaks the erun-mcp WebSocket attach edge's own wire protocol
// directly from the browser (see erun-mcp/AGENTS.md's "The WebSocket Attach
// Edge Is Not An MCP Tool" and erun-mcp/attach.go): binary frames carry raw
// PTY bytes in both directions, text frames carry the two JSON control
// messages (`resize` outbound, `outcome` inbound, sent once immediately
// before the server closes the socket).
//
// A browser's WebSocket constructor cannot set an Authorization header on the
// handshake -- its only credential-bearing surface is the subprotocol list --
// so authentication rides the Sec-WebSocket-Protocol offer instead:
// `new WebSocket(url, [ATTACH_AUTH_SUBPROTOCOL, token])`. This must match
// erun-mcp/auth.go's `attachAuthSubprotocol` exactly, or the edge falls back
// to "no bearer token" and refuses the handshake with 401.
const ATTACH_AUTH_SUBPROTOCOL = 'erun.bearer.v1';

// attachEdgeUrl builds the attach WebSocket endpoint from the same bare
// hostname mcpEdgeUrl resolves the JSON-RPC endpoint from (see liveClient.ts)
// -- tolerant of a pasted scheme or a pasted /mcp path, so a hostname copied
// from one form works for the other.
export function attachEdgeUrl(hostname: string, session: string): string {
  const trimmed = hostname
    .trim()
    .replace(/\/+$/, '')
    .replace(/^wss?:\/\//i, '')
    .replace(/^https?:\/\//i, '')
    .replace(/\/mcp\/?$/i, '');
  return `wss://${trimmed}/mcp/attach/${encodeURIComponent(session)}`;
}

interface AttachOutcomeMessage {
  type: string;
  outcome: string;
}

function parseControlMessage(raw: string): AttachOutcomeMessage | null {
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!isRecord(parsed) || parsed.type !== 'outcome') {
      return null;
    }
    return {
      type: 'outcome',
      outcome: typeof parsed.outcome === 'string' ? parsed.outcome : 'unknown',
    };
  } catch {
    // Not a control message this client recognises -- ignored, same as an
    // unrecognised binary frame would be impossible to produce server-side.
    return null;
  }
}

export interface AttachCallbacks {
  // onOpen fires once the WebSocket handshake succeeds -- the point at which
  // send/resize become meaningful to call.
  onOpen: () => void;
  // onData delivers one binary frame's worth of raw PTY output.
  onData: (chunk: Uint8Array) => void;
  // onOutcome delivers the one outcome message the server sends immediately
  // before it closes the socket -- see eruncommon.AISessionAttachOutcome.
  // "unknown" if the socket closed without one (a network drop, not a
  // reported outcome) so the caller never mistakes silence for success.
  onOutcome: (outcome: string) => void;
  // onError reports a transport-level failure (the handshake itself failing,
  // e.g. a wrong hostname or an expired token).
  onError: (message: string) => void;
}

export interface AttachHandle {
  // send writes raw bytes to the PTY, e.g. a UTF-8-encoded keystroke.
  send: (bytes: Uint8Array) => void;
  // resize tells the far end's PTY the new terminal size.
  resize: (cols: number, rows: number) => void;
  // close ends the session from this side without waiting for an outcome.
  close: () => void;
}

// openAttachSession opens one WebSocket to a deployed environment's attach
// edge and wires the wire protocol above to callbacks. It never resolves a
// token itself -- the caller mints one with the erun:attach scope via
// mcpApi.ts first, the same separation liveClient.ts keeps between minting
// and driving.
export function openAttachSession(
  hostname: string,
  token: string,
  session: string,
  callbacks: AttachCallbacks,
): AttachHandle {
  const socket = new WebSocket(attachEdgeUrl(hostname, session), [ATTACH_AUTH_SUBPROTOCOL, token]);
  socket.binaryType = 'arraybuffer';

  let outcomeReported = false;
  const reportOutcome = (outcome: string): void => {
    outcomeReported = true;
    callbacks.onOutcome(outcome);
  };

  socket.onopen = () => {
    callbacks.onOpen();
  };
  socket.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
    if (typeof event.data === 'string') {
      const control = parseControlMessage(event.data);
      if (control !== null) {
        reportOutcome(control.outcome);
      }
      return;
    }
    callbacks.onData(new Uint8Array(event.data));
  };
  socket.onerror = () => {
    callbacks.onError(`could not reach ${hostname}`);
  };
  // A close with no prior outcome message is a dropped connection, not a
  // reported result -- surfaced as "unknown" rather than left silent, so the
  // caller never mistakes a network stall for a definite outcome.
  socket.onclose = () => {
    if (!outcomeReported) {
      reportOutcome('unknown');
    }
  };

  return {
    send: (bytes: Uint8Array) => {
      socket.send(bytes);
    },
    resize: (cols: number, rows: number) => {
      socket.send(JSON.stringify({ type: 'resize', cols, rows }));
    },
    close: () => {
      socket.close();
    },
  };
}

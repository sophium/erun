import { isRecord } from 'erun-kit';

// Binary frames carry raw PTY bytes; text frames carry `resize` out and `outcome` in.
// Browsers cannot set Authorization on a WebSocket handshake, so the token rides the
// subprotocol offer; this must match erun-mcp's attachAuthSubprotocol or the edge 401s.
const ATTACH_AUTH_SUBPROTOCOL = 'erun.bearer.v1';

// Tolerates a pasted scheme or /mcp path, so one hostname works for both edges.
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
    return null;
  }
}

export interface AttachCallbacks {
  onOpen: () => void;
  onData: (chunk: Uint8Array) => void;
  // "unknown" when the socket closed without an outcome, so silence never reads as success.
  onOutcome: (outcome: string) => void;
  onError: (message: string) => void;
}

export interface AttachHandle {
  send: (bytes: Uint8Array) => void;
  resize: (cols: number, rows: number) => void;
  close: () => void;
}

// The caller mints the erun:attach token first.
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

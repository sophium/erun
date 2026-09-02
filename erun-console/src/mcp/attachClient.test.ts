import { afterEach, describe, expect, it, vi } from 'vitest';

import { attachEdgeUrl, openAttachSession } from './attachClient';

describe('attachEdgeUrl', () => {
  it('builds a wss URL with the /mcp/attach/{session} path from a bare hostname', () => {
    expect(attachEdgeUrl('mcp.acme-prod.services.example.com', 'sess-1')).toBe(
      'wss://mcp.acme-prod.services.example.com/mcp/attach/sess-1',
    );
  });

  it('tolerates a pasted https scheme, trailing slash, and /mcp path', () => {
    expect(attachEdgeUrl('https://mcp.acme-prod.services.example.com/mcp/', 'sess-1')).toBe(
      'wss://mcp.acme-prod.services.example.com/mcp/attach/sess-1',
    );
  });

  it('encodes the session id', () => {
    expect(attachEdgeUrl('mcp.example.com', 'a b')).toBe('wss://mcp.example.com/mcp/attach/a%20b');
  });
});

// FakeWebSocket stands in for the real browser WebSocket so tests can drive
// the wire protocol without a live edge. It records the constructor args
// (url, protocols) so a test can assert the auth subprotocol offer, and
// exposes the same on* handler slots openAttachSession assigns.
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  url: string;
  protocols: string[];
  binaryType = 'blob';
  sent: (string | Uint8Array)[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: ArrayBuffer | string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(url: string, protocols: string[]) {
    this.url = url;
    this.protocols = protocols;
    FakeWebSocket.instances.push(this);
  }

  send(data: string | Uint8Array): void {
    this.sent.push(data);
  }

  close(): void {
    this.closed = true;
    this.onclose?.();
  }

  emitBinary(bytes: Uint8Array): void {
    this.onmessage?.({ data: bytes.buffer as ArrayBuffer });
  }

  emitText(text: string): void {
    this.onmessage?.({ data: text });
  }
}

afterEach(() => {
  vi.unstubAllGlobals();
  FakeWebSocket.instances = [];
});

function stubWebSocket(): typeof FakeWebSocket {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  return FakeWebSocket;
}

// firstSocket asserts the constructor already ran -- openAttachSession
// constructs its WebSocket synchronously, so this never actually throws in a
// passing test, but it turns a missing instance into a clear failure instead
// of a null-access TypeScript would otherwise force awkward optional chains for.
function firstSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances[0];
  if (socket === undefined) {
    throw new Error('expected openAttachSession to construct a WebSocket');
  }
  return socket;
}

describe('openAttachSession', () => {
  it('offers the auth subprotocol and the token, and forwards binary output as onData', () => {
    stubWebSocket();
    const onData = vi.fn();
    openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen: vi.fn(),
      onData,
      onOutcome: vi.fn(),
      onError: vi.fn(),
    });

    const socket = firstSocket();
    expect(socket).toBeDefined();
    expect(socket.url).toBe('wss://mcp.example.com/mcp/attach/sess-1');
    expect(socket.protocols).toEqual(['erun.bearer.v1', 'tok-123']);
    expect(socket.binaryType).toBe('arraybuffer');

    socket.emitBinary(new TextEncoder().encode('hello'));
    const received = onData.mock.calls[0]?.[0] as Uint8Array | undefined;
    expect(received && new TextDecoder().decode(received)).toBe('hello');
  });

  it('fires onOpen once the handshake succeeds', () => {
    stubWebSocket();
    const onOpen = vi.fn();
    openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen,
      onData: vi.fn(),
      onOutcome: vi.fn(),
      onError: vi.fn(),
    });

    firstSocket().onopen?.();
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('reports the outcome text frame and does not re-report it on close', () => {
    stubWebSocket();
    const onOutcome = vi.fn();
    openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen: vi.fn(),
      onData: vi.fn(),
      onOutcome,
      onError: vi.fn(),
    });

    const socket = firstSocket();
    socket.emitText(JSON.stringify({ type: 'outcome', outcome: 'ended' }));
    socket.close();

    expect(onOutcome).toHaveBeenCalledTimes(1);
    expect(onOutcome).toHaveBeenCalledWith('ended');
  });

  it('reports "unknown" on a close with no prior outcome message', () => {
    stubWebSocket();
    const onOutcome = vi.fn();
    openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen: vi.fn(),
      onData: vi.fn(),
      onOutcome,
      onError: vi.fn(),
    });

    firstSocket().close();

    expect(onOutcome).toHaveBeenCalledWith('unknown');
  });

  it('reports a transport error via onError', () => {
    stubWebSocket();
    const onError = vi.fn();
    openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen: vi.fn(),
      onData: vi.fn(),
      onOutcome: vi.fn(),
      onError,
    });

    firstSocket().onerror?.();
    expect(onError).toHaveBeenCalledWith('could not reach mcp.example.com');
  });

  it('send writes raw bytes and resize writes a JSON control message', () => {
    stubWebSocket();
    const handle = openAttachSession('mcp.example.com', 'tok-123', 'sess-1', {
      onOpen: vi.fn(),
      onData: vi.fn(),
      onOutcome: vi.fn(),
      onError: vi.fn(),
    });

    const bytes = new TextEncoder().encode('echo hi\n');
    handle.send(bytes);
    handle.resize(120, 40);
    handle.close();

    const socket = firstSocket();
    expect(socket.sent[0]).toBe(bytes);
    expect(socket.sent[1]).toBe(JSON.stringify({ type: 'resize', cols: 120, rows: 40 }));
    expect(socket.closed).toBe(true);
  });
});

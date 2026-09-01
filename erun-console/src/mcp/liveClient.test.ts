import { afterEach, describe, expect, it, vi } from 'vitest';

import { callMcpTool, mcpEdgeUrl } from './liveClient';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('mcpEdgeUrl', () => {
  it('adds a scheme and the /mcp path to a bare hostname', () => {
    expect(mcpEdgeUrl('mcp.acme-prod.services.example.com')).toBe(
      'https://mcp.acme-prod.services.example.com/mcp',
    );
  });

  it('tolerates a pasted scheme and trailing slash', () => {
    expect(mcpEdgeUrl('https://mcp.acme-prod.services.example.com/')).toBe(
      'https://mcp.acme-prod.services.example.com/mcp',
    );
  });

  it('does not double the /mcp path when the operator already pasted it', () => {
    expect(mcpEdgeUrl('mcp.acme-prod.services.example.com/mcp')).toBe(
      'https://mcp.acme-prod.services.example.com/mcp',
    );
  });
});

interface RecordedRequest {
  url: string;
  headers: Record<string, string>;
  body: unknown;
}

function jsonResponse(body: unknown, init?: { status?: number; sessionId?: string }): Response {
  const status = init?.status ?? 200;
  const headers = new Headers();
  if (init?.sessionId !== undefined) {
    headers.set('Mcp-Session-Id', init.sessionId);
  }
  return {
    ok: status >= 200 && status < 300,
    status,
    headers,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

function emptyResponse(status: number): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers(),
    json: () => Promise.reject(new Error('no body')),
    text: () => Promise.resolve(''),
  } as unknown as Response;
}

function mockSequence(handlers: ((req: RecordedRequest) => Response)[]): RecordedRequest[] {
  const calls: RecordedRequest[] = [];
  let index = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL, init?: RequestInit) => {
      const request: RecordedRequest = {
        url: input instanceof URL ? input.href : input,
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: init?.body !== undefined ? JSON.parse(init.body as string) : undefined,
      };
      calls.push(request);
      const handler = handlers[index];
      index += 1;
      if (handler === undefined) {
        throw new Error(`unexpected extra request: ${JSON.stringify(request)}`);
      }
      return Promise.resolve(handler(request));
    }),
  );
  return calls;
}

describe('callMcpTool', () => {
  it('runs the initialize -> notifications/initialized -> tools/call handshake and returns the text content', async () => {
    const calls = mockSequence([
      () =>
        jsonResponse(
          { jsonrpc: '2.0', id: 1, result: { protocolVersion: '2025-06-18' } },
          { sessionId: 'session-1' },
        ),
      () => emptyResponse(202),
      () =>
        jsonResponse({
          jsonrpc: '2.0',
          id: 2,
          result: { isError: false, content: [{ type: 'text', text: '{"version":"1.2.3"}' }] },
        }),
    ]);

    const result = await callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version');

    expect(result).toEqual({ isError: false, text: '{"version":"1.2.3"}' });
    expect(calls).toHaveLength(3);
    expect(calls[0]?.url).toBe('https://mcp.acme-prod.services.example.com/mcp');
    expect((calls[0]?.body as { method: string }).method).toBe('initialize');
    expect(calls[1]?.headers['Mcp-Session-Id']).toBe('session-1');
    expect((calls[1]?.body as { method: string }).method).toBe('notifications/initialized');
    expect((calls[2]?.body as { method: string }).method).toBe('tools/call');
    expect(calls[2]?.headers.Authorization).toBe('Bearer the-token');
  });

  it('surfaces a tool-level error result as isError rather than throwing', async () => {
    mockSequence([
      () => jsonResponse({ jsonrpc: '2.0', id: 1, result: {} }, { sessionId: 'session-1' }),
      () => emptyResponse(202),
      () =>
        jsonResponse({
          jsonrpc: '2.0',
          id: 2,
          result: { isError: true, content: [{ type: 'text', text: 'boom' }] },
        }),
    ]);

    const result = await callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version');
    expect(result).toEqual({ isError: true, text: 'boom' });
  });

  it('throws when the initialize response carries no session id', async () => {
    mockSequence([() => jsonResponse({ jsonrpc: '2.0', id: 1, result: {} })]);

    await expect(
      callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version'),
    ).rejects.toThrow('did not return a Mcp-Session-Id header');
  });

  it('throws with the response detail on a non-2xx initialize', async () => {
    mockSequence([() => jsonResponse('unauthorized', { status: 401 })]);

    await expect(
      callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version'),
    ).rejects.toThrow(/responded 401/);
  });

  it('throws the JSON-RPC error message when the server reports one', async () => {
    mockSequence([
      () =>
        jsonResponse(
          {
            jsonrpc: '2.0',
            id: 1,
            error: { code: -32000, message: 'token grants no erun capabilities' },
          },
          { sessionId: 'session-1' },
        ),
    ]);

    await expect(
      callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version'),
    ).rejects.toThrow('token grants no erun capabilities');
  });

  it('wraps a network failure with the URL it was trying to reach', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new Error('NetworkError when attempting to fetch resource'))),
    );

    await expect(
      callMcpTool('mcp.acme-prod.services.example.com', 'the-token', 'version'),
    ).rejects.toThrow('could not reach https://mcp.acme-prod.services.example.com/mcp');
  });
});

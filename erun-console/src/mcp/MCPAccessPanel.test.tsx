import { cleanup, fireEvent, screen } from '@testing-library/react';
import type { Environment } from 'erun-kit';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { MCPAccessPanel } from './MCPAccessPanel';

// fetch is mocked at the boundary so the flow exercises the real client +
// controller against the api-protocol.md contract.

interface MockReq {
  method: string;
  url: string;
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

function requestUrl(input: string | URL): string {
  return input instanceof URL ? input.href : input;
}

function mockFetch(handler: (req: MockReq) => Response): MockReq[] {
  const calls: MockReq[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url: requestUrl(input) });
      return Promise.resolve(handler({ method: init?.method ?? 'GET', url: requestUrl(input) }));
    }),
  );
  return calls;
}

const ENVIRONMENTS: Environment[] = [{ environmentId: 'env-1', name: 'prod', type: 'runtime' }];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('MCPAccessPanel', () => {
  it('POSTs to the env mcp-token endpoint and surfaces the token and audience', async () => {
    const calls = mockFetch(() =>
      jsonResponse({ token: 'signed.jwt.value', audience: 'erun-mcp:acme/prod' }),
    );
    renderWithStore(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);

    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));

    expect(await screen.findByText('erun-mcp:acme/prod')).toBeInTheDocument();
    const tokenField = screen.getByLabelText<HTMLTextAreaElement>('MCP bearer token');
    expect(tokenField.value).toBe('signed.jwt.value');

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments/env-1/mcp-token');
  });

  it('surfaces a 501 when the backend has no signing key configured', async () => {
    mockFetch(() => jsonResponse('mcp token signing is not configured', 501));
    renderWithStore(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);

    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));

    expect(
      await screen.findByText('Could not mint an MCP token: mcp token request failed (501)'),
    ).toBeInTheDocument();
  });

  it('shows an empty state and no mint button when there are no environments', () => {
    renderWithStore(<MCPAccessPanel token="dev-token" environments={[]} />);
    expect(screen.getByText('Register an environment to mint an MCP token.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).toBeNull();
  });
});

// jsonResponseWithHeaders backs the "drive this environment" flow below: it
// needs response headers (Mcp-Session-Id) that the plain jsonResponse() above
// never has to carry.
function jsonResponseWithHeaders(body: unknown, headers: Record<string, string> = {}): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers(headers),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

describe('MCPAccessPanel driving a tool over the live edge', () => {
  it('mints a token, then calls the version tool against the operator-supplied MCP hostname', async () => {
    mockFetch(() => jsonResponse({ token: 'signed.jwt.value', audience: 'erun-mcp:acme/prod' }));
    renderWithStore(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));
    await screen.findByText('erun-mcp:acme/prod');

    const liveCalls: { url: string; body: { method: string }; headers: HeadersInit | undefined }[] =
      [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL, init?: RequestInit) => {
        const url = input instanceof URL ? input.href : input;
        const body = JSON.parse(init?.body as string) as { method: string };
        liveCalls.push({ url, body, headers: init?.headers });
        if (body.method === 'initialize') {
          return Promise.resolve(
            jsonResponseWithHeaders(
              { jsonrpc: '2.0', id: 1, result: {} },
              { 'Mcp-Session-Id': 'session-1' },
            ),
          );
        }
        if (body.method === 'notifications/initialized') {
          return Promise.resolve(jsonResponseWithHeaders(''));
        }
        return Promise.resolve(
          jsonResponseWithHeaders({
            jsonrpc: '2.0',
            id: 2,
            result: { isError: false, content: [{ type: 'text', text: '{"version":"1.2.3"}' }] },
          }),
        );
      }),
    );

    fireEvent.change(screen.getByLabelText(/MCP hostname/), {
      target: { value: 'mcp.acme-prod.services.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Call the version tool' }));

    expect(await screen.findByText('{"version":"1.2.3"}')).toBeInTheDocument();
    expect(liveCalls).toHaveLength(3);
    expect(liveCalls.every((c) => c.url === 'https://mcp.acme-prod.services.example.com/mcp')).toBe(
      true,
    );
    expect((liveCalls[2]?.headers as Record<string, string>).Authorization).toBe(
      'Bearer signed.jwt.value',
    );
  });

  it('shows an actionable error when the live edge is unreachable', async () => {
    mockFetch(() => jsonResponse({ token: 'signed.jwt.value', audience: 'erun-mcp:acme/prod' }));
    renderWithStore(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));
    await screen.findByText('erun-mcp:acme/prod');

    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new Error('NetworkError when attempting to fetch resource'))),
    );

    fireEvent.change(screen.getByLabelText(/MCP hostname/), {
      target: { value: 'mcp.not-exposed.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Call the version tool' }));

    expect(await screen.findByText(/Could not call the tool: could not reach/)).toBeInTheDocument();
  });
});

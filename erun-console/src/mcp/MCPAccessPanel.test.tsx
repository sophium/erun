import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Environment } from '../config/types';
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
    render(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);

    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));

    expect(await screen.findByText('erun-mcp:acme/prod')).toBeInTheDocument();
    const tokenField = screen.getByLabelText<HTMLTextAreaElement>('MCP bearer token');
    expect(tokenField.value).toBe('signed.jwt.value');

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments/env-1/mcp-token');
  });

  it('surfaces a 501 when the backend has no signing key configured', async () => {
    mockFetch(() => jsonResponse('mcp token signing is not configured', 501));
    render(<MCPAccessPanel token="dev-token" environments={ENVIRONMENTS} />);

    fireEvent.click(screen.getByRole('button', { name: 'Generate MCP token' }));

    expect(
      await screen.findByText('Could not mint an MCP token: mcp token request failed (501)'),
    ).toBeInTheDocument();
  });

  it('shows an empty state and no mint button when there are no environments', () => {
    render(<MCPAccessPanel token="dev-token" environments={[]} />);
    expect(screen.getByText('Register an environment to mint an MCP token.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).toBeNull();
  });
});

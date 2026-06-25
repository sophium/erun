import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Environment } from '../config/types';
import { MCPAccessPanel } from './MCPAccessPanel';

// Component tests for the MCP-access panel. `fetch` is mocked per request so each
// flow exercises the real client + controller against the documented API shape
// (api-protocol.md): POST /v1/environments/{id}/mcp-token → 200 {token, audience}.

interface MockReq {
  method: string;
  url: string;
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(typeof body === 'string' ? body : JSON.stringify(body)),
  } as unknown as Response;
}

function mockFetch(handler: (req: MockReq) => Response): MockReq[] {
  const calls: MockReq[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL, init?: RequestInit) => {
      const req: MockReq = {
        method: init?.method ?? 'GET',
        url: input instanceof URL ? input.href : input,
      };
      calls.push(req);
      return Promise.resolve(handler(req));
    }),
  );
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const DEPLOYED_ENV: Environment = {
  environmentId: 'env-1',
  name: 'prod',
  type: 'runtime',
  contextId: 'ctx-1',
  runtimeVersion: '1.2.3',
  deployStatus: 'deployed',
  deployedVersion: '1.2.3',
};

describe('MCPAccessPanel', () => {
  it('only offers access for deployed runtime environments', () => {
    mockFetch(() => jsonResponse({}));
    const registered: Environment = { ...DEPLOYED_ENV, deployStatus: 'registered' };
    render(<MCPAccessPanel token="dev-token" environments={[registered]} />);
    expect(screen.getByText('No deployed environments to connect to.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Get MCP access token' })).not.toBeInTheDocument();
  });

  it('mints a token and shows it plus the per-env audience', async () => {
    const calls = mockFetch(() =>
      jsonResponse({ token: 'the.signed.jwt', audience: 'erun-mcp:operations/prod' }),
    );
    render(<MCPAccessPanel token="dev-token" environments={[DEPLOYED_ENV]} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Get MCP access token' }));
      await Promise.resolve();
    });

    expect(screen.getByDisplayValue('the.signed.jwt')).toBeInTheDocument();
    expect(
      screen.getByText(
        "Token scoped to erun-mcp:operations/prod. Present it as a Bearer token to the environment's MCP edge.",
      ),
    ).toBeInTheDocument();
    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments/env-1/mcp-token');
  });

  it('surfaces a mint failure', async () => {
    mockFetch(() => jsonResponse('failed to mint mcp token', 500));
    render(<MCPAccessPanel token="dev-token" environments={[DEPLOYED_ENV]} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Get MCP access token' }));
      await Promise.resolve();
    });
    expect(
      screen.getByText('Request failed: mcp token request failed (500): failed to mint mcp token'),
    ).toBeInTheDocument();
  });
});

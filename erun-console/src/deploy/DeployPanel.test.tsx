import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Environment } from '../config/types';
import { DeployPanel } from './DeployPanel';

// Component tests for the deploy panel. `fetch` is mocked per request (matched
// on method + URL) so each flow exercises the real client + controller against
// the documented API shapes (api-protocol.md): POST /v1/environments/{id}/deploy
// → 202 with the env at deploy_status `deploying`, then GET /v1/environments/{id}
// polled until `deployed`/`failed`.

interface MockReq {
  method: string;
  url: string;
  body: unknown;
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(typeof body === 'string' ? body : JSON.stringify(body)),
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
      const req: MockReq = {
        method: init?.method ?? 'GET',
        url: requestUrl(input),
        body: init?.body !== undefined ? JSON.parse(init.body as string) : undefined,
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
  vi.useRealTimers();
});

const RUNTIME_ENV: Environment = {
  environmentId: 'env-1',
  name: 'prod',
  type: 'runtime',
  contextId: 'ctx-1',
  runtimeVersion: '1.2.3',
  deployStatus: 'registered',
};

const DEPLOYING_ENV: Environment = { ...RUNTIME_ENV, deployStatus: 'deploying' };
const DEPLOYED_ENV: Environment = { ...RUNTIME_ENV, deployStatus: 'deployed', deployedVersion: '1.2.3' };

describe('DeployPanel', () => {
  it('only offers a Deploy action for runtime environments', () => {
    mockFetch(() => jsonResponse({}));
    const agentEnv: Environment = { environmentId: 'env-2', name: 'dev', type: 'remote-agent' };
    render(<DeployPanel token="dev-token" environments={[agentEnv]} />);
    expect(screen.getByText('No runtime environments to deploy.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Deploy' })).not.toBeInTheDocument();
  });

  it('POSTs the deploy then polls getEnvironment and shows the final deployed status', async () => {
    vi.useFakeTimers();
    // POST → 202 deploying; first GET still deploying (so the poll loop runs),
    // second GET deployed (terminal).
    let getCount = 0;
    const calls = mockFetch((req) => {
      if (req.method === 'POST') {
        return jsonResponse(DEPLOYING_ENV, 202);
      }
      getCount += 1;
      return getCount === 1 ? jsonResponse(DEPLOYING_ENV) : jsonResponse(DEPLOYED_ENV);
    });
    render(<DeployPanel token="dev-token" environments={[RUNTIME_ENV]} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByText('Deploying prod…')).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(screen.getByText('prod is deployed (1.2.3).')).toBeInTheDocument();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments/env-1/deploy');
    // The version input defaults to the env's runtimeVersion, so the deploy
    // threads it explicitly.
    expect(post?.body).toEqual({ version: '1.2.3' });
    expect(calls.some((c) => c.method === 'GET' && c.url === '/v1/environments/env-1')).toBe(true);
  });

  it('threads an explicit version override from the input', async () => {
    const calls = mockFetch((req) =>
      req.method === 'POST' ? jsonResponse(DEPLOYED_ENV, 202) : jsonResponse(DEPLOYED_ENV),
    );
    render(<DeployPanel token="dev-token" environments={[RUNTIME_ENV]} />);

    fireEvent.change(screen.getByLabelText('Version'), { target: { value: '2.0.0' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
      await Promise.resolve();
    });

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.body).toEqual({ version: '2.0.0' });
  });

  it('shows the failed status plus the deploy error from the poll', async () => {
    const failed: Environment = {
      ...RUNTIME_ENV,
      deployStatus: 'failed',
      deployError: 'runtime chart 1.2.3 could not be pulled',
    };
    mockFetch((req) => (req.method === 'POST' ? jsonResponse(failed, 202) : jsonResponse(failed)));
    render(<DeployPanel token="dev-token" environments={[RUNTIME_ENV]} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
      await Promise.resolve();
    });
    // The 202 already carries `failed`, so the terminal state shows without
    // polling — both the status line and the reason render.
    expect(screen.getByText('prod failed to deploy.')).toBeInTheDocument();
    expect(screen.getByText('runtime chart 1.2.3 could not be pulled')).toBeInTheDocument();
  });

  it('surfaces a 409 when the context is not provisioned', async () => {
    mockFetch(() => jsonResponse('context is not provisioned', 409));
    render(<DeployPanel token="dev-token" environments={[RUNTIME_ENV]} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
      await Promise.resolve();
    });
    expect(
      screen.getByText('Request failed: deploy request failed (409): context is not provisioned'),
    ).toBeInTheDocument();
  });

  it('surfaces a persisted failed deploy + its reason on first paint (no click)', () => {
    mockFetch(() => jsonResponse({}));
    const failedEnv: Environment = {
      ...RUNTIME_ENV,
      deployStatus: 'failed',
      deployError: 'runtime chart 1.2.3 could not be pulled',
    };
    render(<DeployPanel token="dev-token" environments={[failedEnv]} />);
    // Without any in-session deploy, the persisted failure + reason must show,
    // not a bare Deploy button.
    expect(screen.getByText('prod failed to deploy.')).toBeInTheDocument();
    expect(screen.getByText('runtime chart 1.2.3 could not be pulled')).toBeInTheDocument();
  });
});

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ProvisionPanel } from './ProvisionPanel';

// fetch is mocked at the boundary so each flow exercises the real client +
// controller against the api-protocol.md contract.

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
  } as unknown as Response;
}

// A 204 has no JSON body; calling .json() would throw, matching the real fetch.
function noContentResponse(): Response {
  return {
    ok: true,
    status: 204,
    json: () => Promise.reject(new Error('204 has no body')),
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

const RUNNING_CONTEXT = {
  contextId: 'ctx-9',
  name: 'primary',
  provider: 'aws',
  cloudProviderAlias: 'aws-acme',
  region: 'eu-west-2',
  kubernetesContext: 'primary',
  status: 'running',
};

const PROVISIONING_CONTEXT = { ...RUNNING_CONTEXT, status: 'provisioning' };

describe('ProvisionPanel alias form', () => {
  it('PUTs the BYO-cloud credentials to /v1/cloud-provider-aliases/{alias}', async () => {
    const calls = mockFetch(() => noContentResponse());
    render(<ProvisionPanel token="dev-token" />);

    fireEvent.change(screen.getByLabelText('Alias name'), { target: { value: 'aws-acme' } });
    fireEvent.change(
      screen.getByLabelText('BYO-cloud credentials JSON (stored encrypted server-side)'),
      { target: { value: '{"accessKeyId":"AK","secretAccessKey":"SK"}' } },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));

    expect(
      await screen.findByText('Credentials saved (encrypted server-side).'),
    ).toBeInTheDocument();

    const put = calls.find((c) => c.method === 'PUT');
    expect(put).toBeDefined();
    expect(put?.url).toBe('/v1/cloud-provider-aliases/aws-acme');
    expect(put?.body).toEqual({
      provider: 'aws',
      credentials: '{"accessKeyId":"AK","secretAccessKey":"SK"}',
    });
  });

  it('surfaces a 400 alias error', async () => {
    mockFetch(() => jsonResponse('credentials empty', 400));
    render(<ProvisionPanel token="dev-token" />);

    fireEvent.change(screen.getByLabelText('Alias name'), { target: { value: 'aws-acme' } });
    fireEvent.change(
      screen.getByLabelText('BYO-cloud credentials JSON (stored encrypted server-side)'),
      { target: { value: 'x' } },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));

    expect(
      await screen.findByText('Could not save credentials: alias request failed (400)'),
    ).toBeInTheDocument();
  });
});

describe('ProvisionPanel create-context flow', () => {
  it('POSTs the context then polls getContext and shows the final running status', async () => {
    vi.useFakeTimers();
    // First GET stays provisioning so the poll loop iterates before the terminal running.
    let getCount = 0;
    const calls = mockFetch((req) => {
      if (req.method === 'POST') {
        return jsonResponse({ context: PROVISIONING_CONTEXT, plan: [] }, 202);
      }
      getCount += 1;
      return getCount === 1 ? jsonResponse(PROVISIONING_CONTEXT) : jsonResponse(RUNNING_CONTEXT);
    });
    render(<ProvisionPanel token="dev-token" />);

    fireEvent.change(screen.getByLabelText('Context name'), { target: { value: 'primary' } });
    fireEvent.change(screen.getByLabelText('Cloud provider alias'), {
      target: { value: 'aws-acme' },
    });
    fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'eu-west-2' } });

    // Flush the POST + first poll's promise resolutions inside act so React
    // applies the state updates before we assert.
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Provision context' }));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByText('Provisioning primary…')).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(screen.getByText('primary is running.')).toBeInTheDocument();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/contexts');
    expect(post?.body).toEqual({
      name: 'primary',
      cloudProviderAlias: 'aws-acme',
      region: 'eu-west-2',
    });
    expect(calls.some((c) => c.method === 'GET' && c.url === '/v1/contexts/ctx-9')).toBe(true);
  });

  it('shows the failed status plus the provision error from the poll', async () => {
    const failed = {
      ...RUNNING_CONTEXT,
      status: 'failed',
      provisionError: 'run-instances: InsufficientInstanceCapacity',
    };
    mockFetch((req) =>
      req.method === 'POST'
        ? jsonResponse({ context: failed, plan: [] }, 202)
        : jsonResponse(failed),
    );
    render(<ProvisionPanel token="dev-token" />);

    fireEvent.change(screen.getByLabelText('Context name'), { target: { value: 'primary' } });
    fireEvent.change(screen.getByLabelText('Cloud provider alias'), {
      target: { value: 'aws-acme' },
    });
    fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'eu-west-2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Provision context' }));

    // The 202 already carries `failed`, so the terminal state renders without polling.
    expect(await screen.findByText('primary failed to provision.')).toBeInTheDocument();
    expect(screen.getByText('run-instances: InsufficientInstanceCapacity')).toBeInTheDocument();
  });
});

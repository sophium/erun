import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { CloudContext } from '../config/types';
import { RegisterEnvPanel } from './RegisterEnvPanel';

// Component tests for the env-registration panel. `fetch` is mocked per request
// (matched on method + URL) so each flow exercises the real client + controller
// against the documented API shape (api-protocol.md): POST /v1/environments →
// 201 with the persisted env, 409 on the environment quota, 400 on invalid input.

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
});

const RUNNING_CONTEXT: CloudContext = {
  contextId: 'ctx-1',
  name: 'primary',
  provider: 'aws',
  region: 'eu-west-2',
  status: 'running',
};

const CREATED_ENV = {
  environmentId: 'env-9',
  name: 'prod',
  type: 'runtime',
  contextId: 'ctx-1',
  runtimeVersion: '1.2.3',
  deployStatus: 'registered',
};

function fillForm(): void {
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'prod' } });
  fireEvent.change(screen.getByLabelText('Cloud context'), { target: { value: 'ctx-1' } });
  fireEvent.change(screen.getByLabelText('Runtime version (optional)'), {
    target: { value: '1.2.3' },
  });
}

describe('RegisterEnvPanel', () => {
  it('POSTs the environment and reports success + refreshes on 201', async () => {
    const calls = mockFetch(() => jsonResponse(CREATED_ENV, 201));
    const onRegistered = vi.fn();
    render(<RegisterEnvPanel token="dev-token" contexts={[RUNNING_CONTEXT]} onRegistered={onRegistered} />);

    fillForm();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));
      await Promise.resolve();
    });

    expect(await screen.findByText('Environment prod registered.')).toBeInTheDocument();
    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments');
    expect(post?.body).toEqual({
      name: 'prod',
      type: 'runtime',
      contextId: 'ctx-1',
      runtimeVersion: '1.2.3',
    });
    // On success the parent is asked to refresh the read model so the new env
    // shows up in the config view + deploy panel.
    expect(onRegistered).toHaveBeenCalledTimes(1);
  });

  it('defaults type to runtime and omits an empty context/version', async () => {
    const calls = mockFetch(() => jsonResponse(CREATED_ENV, 201));
    render(<RegisterEnvPanel token="dev-token" contexts={[]} onRegistered={vi.fn()} />);

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'prod' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));
      await Promise.resolve();
    });

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.body).toEqual({ name: 'prod', type: 'runtime' });
  });

  it('surfaces the 409 environment-quota error and does not refresh', async () => {
    const onRegistered = vi.fn();
    mockFetch(() => jsonResponse('environment quota reached', 409));
    render(<RegisterEnvPanel token="dev-token" contexts={[RUNNING_CONTEXT]} onRegistered={onRegistered} />);

    fillForm();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));
      await Promise.resolve();
    });

    expect(
      await screen.findByText(
        'Could not register environment: create environment request failed (409): environment quota reached',
      ),
    ).toBeInTheDocument();
    expect(onRegistered).not.toHaveBeenCalled();
  });
});

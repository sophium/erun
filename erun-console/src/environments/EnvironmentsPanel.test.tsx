import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import type { Environment } from 'erun-kit';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { EnvironmentsPanel } from './EnvironmentsPanel';

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
  runtimeVersion: '1.2.3',
  status: 'running',
};

describe('EnvironmentsPanel register form', () => {
  it('POSTs the create request with only operator-authored fields (no tenant)', async () => {
    const calls = mockFetch(() =>
      jsonResponse(
        { environmentId: 'env-2', name: 'staging', type: 'runtime', status: 'registered' },
        201,
      ),
    );
    renderWithStore(
      <EnvironmentsPanel token="dev-token" contexts={[]} environments={[]} onChanged={vi.fn()} />,
    );

    fireEvent.change(screen.getByLabelText('Name', { exact: false }), {
      target: { value: 'staging' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));

    expect(await screen.findByText('Environment staging registered.')).toBeInTheDocument();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments');
    expect(post?.body).toEqual({
      name: 'staging',
      type: 'runtime',
      contextId: undefined,
      kubernetesContext: undefined,
      runtimeVersion: undefined,
    });
  });

  it('calls onChanged after a successful register so the parent refreshes the read model', async () => {
    mockFetch(() =>
      jsonResponse({ environmentId: 'env-2', name: 'staging', type: 'runtime' }, 201),
    );
    const onChanged = vi.fn();
    renderWithStore(
      <EnvironmentsPanel token="dev-token" contexts={[]} environments={[]} onChanged={onChanged} />,
    );

    fireEvent.change(screen.getByLabelText('Name', { exact: false }), {
      target: { value: 'staging' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));

    await screen.findByText('Environment staging registered.');
    expect(onChanged).toHaveBeenCalled();
  });

  it('surfaces a 400 register error', async () => {
    mockFetch(() => jsonResponse('name must be a DNS-1123 label', 400));
    renderWithStore(
      <EnvironmentsPanel token="dev-token" contexts={[]} environments={[]} onChanged={vi.fn()} />,
    );

    fireEvent.change(screen.getByLabelText('Name', { exact: false }), {
      target: { value: 'Bad Name' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Register environment' }));

    expect(
      await screen.findByText(
        'Could not register environment: register environment request failed (400)',
      ),
    ).toBeInTheDocument();
  });
});

describe('EnvironmentsPanel deploy flow', () => {
  it('POSTs to the deploy endpoint then polls to running, showing the deployed vs pinned version', async () => {
    vi.useFakeTimers();
    let getCount = 0;
    const deploying = { ...RUNTIME_ENV, status: 'provisioning' as const };
    const running = { ...RUNTIME_ENV, status: 'running' as const, deployedVersion: '1.2.2' };
    const calls = mockFetch((req) => {
      if (req.method === 'POST') {
        return jsonResponse(deploying, 202);
      }
      getCount += 1;
      return getCount === 1 ? jsonResponse(deploying) : jsonResponse(running);
    });
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[RUNTIME_ENV]}
        onChanged={vi.fn()}
      />,
    );

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByText('Deploying prod…')).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(
      screen.getByText('prod is running — deployed 1.2.2 (pinned 1.2.3).'),
    ).toBeInTheDocument();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/v1/environments/env-1/deploy');
    expect(calls.some((c) => c.method === 'GET' && c.url === '/v1/environments/env-1')).toBe(true);
  });

  it('shows a failed deploy with its full, untruncated provision error', async () => {
    const failed = {
      ...RUNTIME_ENV,
      status: 'failed',
      provisionError:
        'deploy job failed for version 1.2.3: probed registries ghcr.io/acme, docker.io/acme — pull denied; publish the version or grant pull access',
    };
    mockFetch((req) => (req.method === 'POST' ? jsonResponse(failed, 202) : jsonResponse(failed)));
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[RUNTIME_ENV]}
        onChanged={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));

    expect(await screen.findByText('prod failed to deploy.')).toBeInTheDocument();
    expect(screen.getByText(failed.provisionError)).toBeInTheDocument();
  });

  it('renders a 409 as an in-flight deploy, not an error toast', async () => {
    mockFetch(() => jsonResponse('a deploy is already in progress for this environment', 409));
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[RUNTIME_ENV]}
        onChanged={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));

    const status = await screen.findByText('A deploy is already in progress for prod.');
    expect(status.closest('[role]')).toHaveAttribute('role', 'status');
  });

  it('renders a 501 by naming the missing deploy executor plainly', async () => {
    mockFetch(() => jsonResponse('the deploy executor is not configured', 501));
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[RUNTIME_ENV]}
        onChanged={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));

    expect(
      await screen.findByText('The deploy executor is not configured on this control plane.'),
    ).toBeInTheDocument();
  });

  it('shows an empty state when there are no runtime environments', () => {
    renderWithStore(
      <EnvironmentsPanel token="dev-token" contexts={[]} environments={[]} onChanged={vi.fn()} />,
    );
    expect(screen.getByText('No runtime environments to deploy.')).toBeInTheDocument();
  });

  // #1170: the API refuses a deploy on a row whose delete is outstanding, so
  // offering the button meant an operator clicked it and got a raw Kubernetes
  // admission error back. Before the API guard existed it was worse: the deploy
  // was accepted and overwrote the teardown state.
  it('offers no Deploy control for an environment that is being deleted', () => {
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[{ ...RUNTIME_ENV, status: 'deleting' }]}
        onChanged={vi.fn()}
      />,
    );

    expect(screen.queryByRole('button', { name: 'Deploy' })).not.toBeInTheDocument();
    expect(
      screen.getByText('prod is being deleted, so it cannot be deployed.'),
    ).toBeInTheDocument();
  });

  it('surfaces the recorded blocker for a deletion-blocked environment instead of a Deploy button', () => {
    const blocker =
      'namespace "operations-prod" did not finish terminating within 20m0s:\nNamespaceFinalizersRemaining=True  acme.cert-manager.io/finalizer in 1 resource instances';
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[{ ...RUNTIME_ENV, status: 'deletion-blocked', deleteError: blocker }]}
        onChanged={vi.fn()}
      />,
    );

    expect(screen.queryByRole('button', { name: 'Deploy' })).not.toBeInTheDocument();
    expect(screen.getByText(/delete is blocked and still outstanding/)).toBeInTheDocument();
    // The blocker is what an operator can act on -- it names the finalizer
    // actually holding the namespace -- so it is shown whole, not summarised.
    // Matched by substring because Testing Library collapses the newlines.
    expect(screen.getByText(/did not finish terminating within 20m0s/)).toBeInTheDocument();
    expect(
      screen.getByText(/acme\.cert-manager\.io\/finalizer in 1 resource instances/),
    ).toBeInTheDocument();
  });

  it('still offers Deploy for every non-teardown status', () => {
    for (const status of ['registered', 'running', 'failed'] as const) {
      cleanup();
      renderWithStore(
        <EnvironmentsPanel
          token="dev-token"
          contexts={[]}
          environments={[{ ...RUNTIME_ENV, status }]}
          onChanged={vi.fn()}
        />,
      );
      expect(screen.getByRole('button', { name: 'Deploy' })).toBeInTheDocument();
    }
  });
});

// erun#1816: while an OPERATIONS caller is scoped to another tenant, every
// row must name it -- otherwise the row reads as the caller's own.
describe('EnvironmentsPanel tenant scope', () => {
  const OWN_TENANT_ENV: Environment = { ...RUNTIME_ENV, tenantId: 'tenant-own' };
  const OTHER_TENANT = {
    tenantId: 'tenant-other',
    name: 'Beta',
    type: 'COMPANY',
    createdAt: '2026-06-24T10:00:00Z',
    updatedAt: '2026-06-24T10:00:00Z',
  };

  it('renders no tenant badge when no tenant list is available', () => {
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[OWN_TENANT_ENV]}
        onChanged={vi.fn()}
      />,
    );
    expect(screen.queryByText('Beta')).not.toBeInTheDocument();
  });

  it("names a row's owning tenant once the tenant list is available", () => {
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[{ ...RUNTIME_ENV, tenantId: 'tenant-other' }]}
        tenants={[OTHER_TENANT]}
        onChanged={vi.fn()}
      />,
    );
    expect(screen.getByText('Beta')).toBeInTheDocument();
  });

  it("fetches the scoped tenant's own environments instead of the passed-in list", async () => {
    const calls = mockFetch((req) =>
      req.url === '/v1/environments?tenantId=tenant-other'
        ? jsonResponse([{ ...RUNTIME_ENV, environmentId: 'env-2', name: 'staging' }])
        : jsonResponse([], 404),
    );
    renderWithStore(
      <EnvironmentsPanel
        token="dev-token"
        contexts={[]}
        environments={[OWN_TENANT_ENV]}
        tenants={[OTHER_TENANT]}
        scopeTenantId="tenant-other"
        onChanged={vi.fn()}
      />,
    );

    expect(await screen.findByText('staging')).toBeInTheDocument();
    expect(screen.queryByText('prod')).not.toBeInTheDocument();
    expect(calls.some((c) => c.url === '/v1/environments?tenantId=tenant-other')).toBe(true);
  });
});

import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from '../App';
import { renderWithStore } from '../test/renderWithStore';

// Mocking `fetch` exercises the whole config read path end-to-end. The OIDC
// login flow is deliberately not covered here — it is a flagged placeholder
// that needs a live IdP to verify.

const SAMPLE_CONFIG = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY' },
  environments: [
    {
      environmentId: 'env-1',
      name: 'prod',
      type: 'runtime',
      kubernetesContext: 'primary',
      contextId: 'ctx-1',
      runtimeVersion: '1.2.3',
      status: 'running',
    },
    {
      environmentId: 'env-2',
      name: 'dev',
      type: 'remote-agent',
      status: 'failed',
      provisionError: 'deploy job did not succeed',
    },
  ],
  contexts: [
    {
      contextId: 'ctx-1',
      name: 'primary',
      provider: 'aws',
      region: 'eu-west-2',
      kubernetesContext: 'primary',
      status: 'running',
    },
    {
      contextId: 'ctx-2',
      name: 'secondary',
      provider: 'aws',
      region: 'us-east-1',
      status: 'failed',
      provisionError: 'run-instances: InsufficientInstanceCapacity',
    },
  ],
};

const EMPTY_CONFIG = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY' },
  environments: [],
  contexts: [],
};

// A pre-#1066 platform: the OPERATIONS tenant is still named "operations",
// but this platform's own declared identity (ERUN_TENANT) is "frs".
const MISMATCHED_OPERATIONS_CONFIG = {
  tenant: { tenantId: 'tn-1', name: 'operations', type: 'OPERATIONS', platformDeclaredName: 'frs' },
  environments: [],
  contexts: [],
};

const RECONCILED_OPERATIONS_CONFIG = {
  tenant: { tenantId: 'tn-1', name: 'frs', type: 'OPERATIONS' },
  environments: [],
  contexts: [],
};

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

function mockFetch(response: Response): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(response)),
  );
}

// mockFetchForReconcile answers every GET /v1/config in sequence from
// configResponses (repeating the last one once exhausted) and every
// reconcile-name POST with reconcileResponse — the shape needed to prove a
// rename converges the view once RTK Query's tag invalidation refetches.
function mockFetchForReconcile(configResponses: unknown[], reconcileResponse: unknown): void {
  let configCallIndex = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn((input: unknown) => {
      const url = typeof input === 'string' ? input : String(input);
      if (url.includes('/v1/tenants/reconcile-name')) {
        return Promise.resolve(jsonResponse(reconcileResponse));
      }
      if (url.includes('/v1/config')) {
        const body = configResponses[Math.min(configCallIndex, configResponses.length - 1)];
        configCallIndex += 1;
        return Promise.resolve(jsonResponse(body));
      }
      // GET /v1/platform: unrelated to this scenario, and its lenient parse
      // tolerates an empty body.
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

beforeEach(() => {
  vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe('ConfigView via App', () => {
  it('renders the tenant, both environments with their types, and the context', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    // The shell's own h1 carries the active section title ("Overview"); the
    // tenant name is a section-level h2 within it.
    expect(await screen.findByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: 'prod' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'dev' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: 'runtime' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'remote-agent' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: '1.2.3' })).toBeInTheDocument();

    // Scoped to the contexts section because the context name "primary" also
    // appears as an env's kubernetes context cell, so an unscoped query is ambiguous.
    const contexts = within(screen.getByRole('region', { name: 'Cloud contexts' }));
    expect(contexts.getByText('primary')).toBeInTheDocument();
    expect(contexts.getByText('aws · eu-west-2')).toBeInTheDocument();
  });

  it('renders a running badge and a failed badge with its provision error', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    const contexts = within(await screen.findByRole('region', { name: 'Cloud contexts' }));

    // The running context's badge is a semantic text label, not color-only.
    expect(contexts.getByText('Running')).toBeInTheDocument();

    // The provision error is visible text, not hidden behind a bare title tooltip.
    expect(contexts.getByText('Failed')).toBeInTheDocument();
    expect(contexts.getByText('run-instances: InsufficientInstanceCapacity')).toBeInTheDocument();
  });

  it('renders environment provisioning status badges, scoped to the environments table', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    const envs = within(await screen.findByRole('region', { name: 'Environments' }));
    expect(envs.getByText('Running')).toBeInTheDocument();
    expect(envs.getByText('Failed')).toBeInTheDocument();
    // A failed env surfaces its provision error inline, like a failed context.
    expect(envs.getByText('deploy job did not succeed')).toBeInTheDocument();
  });

  it('renders empty states for an empty payload', async () => {
    mockFetch(jsonResponse(EMPTY_CONFIG));
    renderWithStore(<App />);

    expect(await screen.findByText('No environments yet.')).toBeInTheDocument();
    expect(screen.getByText('No cloud contexts yet.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();
  });

  it('does not show the tenant-name mismatch banner for a tenant whose name already agrees', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    await screen.findByRole('heading', { level: 2, name: 'Acme' });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('surfaces a legacy-named platform tenant and offers to rename it', async () => {
    mockFetch(jsonResponse(MISMATCHED_OPERATIONS_CONFIG));
    renderWithStore(<App />);

    const banner = await screen.findByRole('alert');
    expect(banner.textContent).toContain('operations');
    expect(banner.textContent).toContain('frs');
    expect(within(banner).getByRole('button', { name: 'Rename to "frs"' })).toBeInTheDocument();
  });

  it('renaming the tenant converges the view once the config refetches', async () => {
    mockFetchForReconcile([MISMATCHED_OPERATIONS_CONFIG, RECONCILED_OPERATIONS_CONFIG], {
      tenant: RECONCILED_OPERATIONS_CONFIG.tenant,
      renamed: true,
    });
    renderWithStore(<App />);

    const banner = await screen.findByRole('alert');
    fireEvent.click(within(banner).getByRole('button', { name: 'Rename to "frs"' }));

    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 2, name: 'frs' })).toBeInTheDocument();
    });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  // A 401 *with* a token held is not a signed-out caller: the identity provider
  // authenticated them and the API still refused, which means the identity is
  // enrolled in no tenant. This test previously asserted the sign-in prompt,
  // which encoded the dead end -- Sign in succeeds and lands right back here
  // (#1167). The signed-out case is covered by the test below, which has no
  // token at all.
  it('tells a signed-in but unenrolled caller they need enrolling, not to sign in again', async () => {
    mockFetch(jsonResponse('invalid bearer token', 401));
    renderWithStore(<App />);

    expect(
      await screen.findByText(
        'You are signed in, but your account is not yet part of a tenant on this platform.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText(/An operator has to enrol you/)).toBeInTheDocument();
    // Offering Sign in here is the loop this fix removes.
    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
  });

  it('renders the landing page when there is no dev token', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', '');
    renderWithStore(<App />);

    await waitFor(() => {
      expect(
        screen.getByText(/A bearer token is required to view your environments\./),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByRole('link', { name: 'configure OIDC sign-in for this instance' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument();
  });
});

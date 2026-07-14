import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from '../App';

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
    render(<App />);

    expect(await screen.findByRole('heading', { level: 1, name: 'Acme' })).toBeInTheDocument();

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
    render(<App />);

    const contexts = within(await screen.findByRole('region', { name: 'Cloud contexts' }));

    // The running context's badge is a semantic text label, not color-only.
    expect(contexts.getByText('Running')).toBeInTheDocument();

    // The provision error is visible text, not hidden behind a bare title tooltip.
    expect(contexts.getByText('Failed')).toBeInTheDocument();
    expect(contexts.getByText('run-instances: InsufficientInstanceCapacity')).toBeInTheDocument();
  });

  it('renders environment provisioning status badges, scoped to the environments table', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    render(<App />);

    const envs = within(await screen.findByRole('region', { name: 'Environments' }));
    expect(envs.getByText('Running')).toBeInTheDocument();
    expect(envs.getByText('Failed')).toBeInTheDocument();
    // A failed env surfaces its provision error inline, like a failed context.
    expect(envs.getByText('deploy job did not succeed')).toBeInTheDocument();
  });

  it('renders empty states for an empty payload', async () => {
    mockFetch(jsonResponse(EMPTY_CONFIG));
    render(<App />);

    expect(await screen.findByText('No environments yet.')).toBeInTheDocument();
    expect(screen.getByText('No cloud contexts yet.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1, name: 'Acme' })).toBeInTheDocument();
  });

  it('renders the sign-in prompt on a 401', async () => {
    mockFetch(jsonResponse('invalid bearer token', 401));
    render(<App />);

    expect(await screen.findByText('Sign in to view your environments.')).toBeInTheDocument();
  });

  it('renders the sign-in prompt when there is no dev token', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', '');
    render(<App />);

    await waitFor(() => {
      expect(screen.getByText('Sign in to view your environments.')).toBeInTheDocument();
    });
  });
});

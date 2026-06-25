import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from '../App';

// The increment's real verification: with a dev token present, App drives
// auth → fetchConfig → ConfigView, so mocking `fetch` exercises the whole read
// path end-to-end (client parse + render). The OIDC flow itself is not covered
// here — it is a flagged placeholder (TODO(#606) in src/auth/auth.ts) that needs
// a live IdP to verify; a Playwright e2e harness like erun-ui's is the follow-up.

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
      deployStatus: 'deployed',
      deployedVersion: '1.2.3',
    },
    {
      environmentId: 'env-2',
      name: 'dev',
      type: 'remote-agent',
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
  // The dev token source App reads to decide whether to fetch at all.
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

    // Both environment names render.
    expect(screen.getByRole('cell', { name: 'prod' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'dev' })).toBeInTheDocument();

    // Both environment types render.
    expect(screen.getByRole('cell', { name: 'runtime' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'remote-agent' })).toBeInTheDocument();

    // The runtime version of the env that has one.
    expect(screen.getByRole('cell', { name: '1.2.3' })).toBeInTheDocument();

    // The cloud context: name + provider/region. Scoped to the contexts section
    // because the context name "primary" also appears as an env's kubernetes
    // context cell (the api-protocol.md sample reuses the name for both).
    const contexts = within(screen.getByRole('region', { name: 'Cloud contexts' }));
    expect(contexts.getByText('primary')).toBeInTheDocument();
    expect(contexts.getByText('aws · eu-west-2')).toBeInTheDocument();
  });

  it('renders a running badge and a failed badge with its provision error', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    render(<App />);

    const contexts = within(await screen.findByRole('region', { name: 'Cloud contexts' }));

    // The running context shows a "Running" badge (semantic text label, not
    // color-only) and no error reason.
    expect(contexts.getByText('Running')).toBeInTheDocument();

    // The failed context shows a "Failed" badge plus the provision error inline
    // (essential info is visible text, not hidden behind a bare title tooltip).
    expect(contexts.getByText('Failed')).toBeInTheDocument();
    expect(contexts.getByText('run-instances: InsufficientInstanceCapacity')).toBeInTheDocument();
  });

  it('renders the env deploy-status badge in the environments table', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    render(<App />);

    // The deployed env shows a "Deployed" badge in the environments table
    // (scoped to that region: the DeployPanel below also reports the deploy).
    const environments = within(await screen.findByRole('region', { name: 'Environments' }));
    expect(environments.getByText('Deployed')).toBeInTheDocument();
  });

  it('renders empty states for an empty payload', async () => {
    mockFetch(jsonResponse(EMPTY_CONFIG));
    render(<App />);

    expect(await screen.findByText('No environments yet.')).toBeInTheDocument();
    expect(screen.getByText('No cloud contexts yet.')).toBeInTheDocument();
    // The tenant header still renders.
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

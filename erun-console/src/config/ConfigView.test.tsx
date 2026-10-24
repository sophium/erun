import { cleanup, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from '../App';
import { renderWithStore } from '../test/renderWithStore';

// Mocks `fetch` to exercise the config read path; OIDC login needs a live IdP and is not covered.

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
    renderWithStore(<App />);

    expect(await screen.findByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: 'prod' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'dev' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: 'runtime' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'remote-agent' })).toBeInTheDocument();

    expect(screen.getByRole('cell', { name: '1.2.3' })).toBeInTheDocument();

    // Scoped because "primary" also appears as an env's kubernetes context cell.
    const contexts = within(screen.getByRole('region', { name: 'Cloud contexts' }));
    expect(contexts.getByText('primary')).toBeInTheDocument();
    expect(contexts.getByText('aws · eu-west-2')).toBeInTheDocument();
  });

  it('renders a running badge and a failed badge with its provision error', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    const contexts = within(await screen.findByRole('region', { name: 'Cloud contexts' }));

    expect(contexts.getByText('Running')).toBeInTheDocument();

    expect(contexts.getByText('Failed')).toBeInTheDocument();
    // Queried by role: the reason appears asynchronously and must be announced.
    const contextsFailure = contexts.getByRole('alert');
    expect(contextsFailure).toHaveTextContent('run-instances: InsufficientInstanceCapacity');
    expect(contextsFailure).toHaveAttribute('aria-live', 'polite');
  });

  it('renders environment provisioning status badges, scoped to the environments table', async () => {
    mockFetch(jsonResponse(SAMPLE_CONFIG));
    renderWithStore(<App />);

    const envs = within(await screen.findByRole('region', { name: 'Environments' }));
    expect(envs.getByText('Running')).toBeInTheDocument();
    expect(envs.getByText('Failed')).toBeInTheDocument();
    const envFailure = envs.getByRole('alert');
    expect(envFailure).toHaveTextContent('deploy job did not succeed');
    expect(envFailure).toHaveAttribute('aria-live', 'polite');
  });

  it('renders empty states for an empty payload', async () => {
    mockFetch(jsonResponse(EMPTY_CONFIG));
    renderWithStore(<App />);

    expect(await screen.findByText('No environments yet.')).toBeInTheDocument();
    expect(screen.getByText('No cloud contexts yet.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();
  });

  // A 401 with a token means unenrolled; a Sign in prompt would loop.
  it('tells a signed-in but unenrolled caller they need enrolling, not to sign in again', async () => {
    mockFetch(jsonResponse('invalid bearer token', 401));
    renderWithStore(<App />);

    expect(
      await screen.findByText(
        'You are signed in, but your account is not yet part of a tenant on this platform.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText(/An operator has to enrol you/)).toBeInTheDocument();
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

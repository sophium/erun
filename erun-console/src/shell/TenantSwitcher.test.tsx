import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Provider } from 'react-redux';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createAppStore } from '../app/store';
import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';
import { consumeTenantSwitchIntent } from './tenantSwitch';
import { TenantSwitcher } from './TenantSwitcher';

vi.mock('../auth/auth', () => ({
  beginLogin: vi.fn(),
}));

const OIDC: OidcConfig = {
  issuer: 'https://auth.acme.example',
  clientId: 'console-client',
  redirectUri: 'http://localhost/',
};

const CURRENT = { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' };

function jsonResponse(body: unknown): Response {
  return { ok: true, status: 200, json: () => Promise.resolve(body) } as unknown as Response;
}

function mockReachable(body: unknown): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(jsonResponse(body))),
  );
}

function renderSwitcher(oidc: OidcConfig | undefined = OIDC): void {
  render(
    <Provider store={createAppStore()}>
      <TenantSwitcher token="dev-token" current={CURRENT} oidc={oidc} />
    </Provider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  sessionStorage.clear();
});

describe('TenantSwitcher', () => {
  it('renders the current tenant as a plain label, never a control, when only one tenant is reachable', async () => {
    mockReachable([{ tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' }]);
    renderSwitcher();

    expect(screen.getByText('Acme')).toBeInTheDocument();
    expect(screen.getByText('Tenant · COMPANY')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();

    await Promise.resolve();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('renders a plain label before the reachable-tenants request resolves, not a premature control', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => undefined)),
    );
    renderSwitcher();

    expect(screen.getByText('Acme')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('renders a real control once the caller maps to more than one tenant', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' },
      { tenantId: 'tenant-b', name: 'Beta', type: 'COMPANY' },
    ]);
    renderSwitcher();

    expect(await screen.findByRole('combobox')).toBeInTheDocument();
  });

  it('never renders a control when there is no OIDC config to switch through, even with multiple reachable tenants', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' },
      { tenantId: 'tenant-b', name: 'Beta', type: 'COMPANY' },
    ]);
    renderSwitcher(undefined);

    await Promise.resolve();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(screen.getByText('Acme')).toBeInTheDocument();
  });

  it('records the intended target and starts a fresh sign-in when another tenant is picked', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' },
      { tenantId: 'tenant-b', name: 'Beta', type: 'COMPANY' },
    ]);
    renderSwitcher();

    fireEvent.click(await screen.findByRole('combobox'));
    fireEvent.click(await screen.findByRole('option', { name: 'Beta' }));

    expect(beginLogin).toHaveBeenCalledWith(OIDC, 'select_account');
    expect(consumeTenantSwitchIntent()).toEqual({ tenantId: 'tenant-b', name: 'Beta' });
  });
  it('never offers a membership the platform reports as unreachable, and names why', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY', reachability: 'RESOLVABLE' },
      { tenantId: 'tenant-b', name: 'Validation', type: 'COMPANY', reachability: 'ORG_MISMATCH' },
    ]);
    renderSwitcher();

    expect(await screen.findByText('Not reachable with this account')).toBeInTheDocument();
    expect(screen.getByText('Validation')).toBeInTheDocument();
    expect(screen.getByText(/different organization/)).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('names a tenant with no org mapping as unconfigured rather than as a wrong-account problem', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY', reachability: 'RESOLVABLE' },
      { tenantId: 'tenant-c', name: 'Probeco', type: 'COMPANY', reachability: 'NO_ORG_MAPPING' },
    ]);
    renderSwitcher();

    expect(
      await screen.findByText(/no organization on its identity-provider mapping/),
    ).toBeInTheDocument();
  });

  it('still offers the resolvable memberships alongside an unreachable one', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY', reachability: 'RESOLVABLE' },
      { tenantId: 'tenant-b', name: 'Beta', type: 'COMPANY', reachability: 'RESOLVABLE' },
      { tenantId: 'tenant-c', name: 'Probeco', type: 'COMPANY', reachability: 'NO_ORG_MAPPING' },
    ]);
    renderSwitcher();

    fireEvent.click(await screen.findByRole('combobox'));
    expect(await screen.findByRole('option', { name: 'Beta' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Probeco' })).not.toBeInTheDocument();
  });

  it('refuses an unrecognized verdict instead of treating it as reachable', async () => {
    mockReachable([
      { tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY', reachability: 'RESOLVABLE' },
      { tenantId: 'tenant-b', name: 'Beta', type: 'COMPANY', reachability: 'SOMETHING_NEW' },
    ]);
    renderSwitcher();

    expect(await screen.findByText(/does not recognize the reason/)).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });
});

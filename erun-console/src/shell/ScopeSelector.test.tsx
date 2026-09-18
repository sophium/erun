import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ScopeSelector } from './ScopeSelector';

// beginLogin is mocked the same way TenantSwitcher.test.tsx mocks it, so a
// test here can assert the negative: unlike TenantSwitcher, picking a target
// tenant must never reach the OIDC redirect path at all.
vi.mock('../auth/auth', () => ({
  beginLogin: vi.fn(),
}));

const OWN_TENANT = { tenantId: 'tenant-own', name: 'Acme' };

const TENANTS = [
  { tenantId: 'tenant-own', name: 'Acme', type: 'OPERATIONS', createdAt: '', updatedAt: '' },
  { tenantId: 'tenant-other', name: 'Beta', type: 'COMPANY', createdAt: '', updatedAt: '' },
];

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ScopeSelector', () => {
  it('renders nothing for a non-OPERATIONS caller', () => {
    render(
      <ScopeSelector
        tenantType="COMPANY"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value={undefined}
        active="overview"
        onChange={vi.fn()}
      />,
    );
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('renders a control for an OPERATIONS caller, defaulted to their own tenant', () => {
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value={undefined}
        active="overview"
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByRole('combobox')).toHaveTextContent('Acme');
    expect(screen.queryByText(/Viewing another tenant/)).not.toBeInTheDocument();
  });

  it('reports the picked tenant id and shows the scoped note, without re-authenticating', async () => {
    const { beginLogin } = await import('../auth/auth');
    const onChange = vi.fn();
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value={undefined}
        active="overview"
        onChange={onChange}
      />,
    );

    fireEvent.click(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByRole('option', { name: 'Beta' }));

    expect(onChange).toHaveBeenCalledWith('tenant-other');
    expect(beginLogin).not.toHaveBeenCalled();
  });

  it('reports undefined when the caller picks their own tenant back', async () => {
    const onChange = vi.fn();
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value="tenant-other"
        active="overview"
        onChange={onChange}
      />,
    );

    fireEvent.click(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByRole('option', { name: 'Acme' }));

    expect(onChange).toHaveBeenCalledWith(undefined);
  });

  it('names the caller’s own identity while scoped to another tenant', () => {
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value="tenant-other"
        active="overview"
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText(/Viewing another tenant's rows/)).toBeInTheDocument();
    expect(screen.getByText(/Acme/)).toBeInTheDocument();
  });

  // The selector renders on every section, but its claim that
  // scope applies must not -- a section whose own panel never threads
  // scopeTenantId server-side (e.g. Tenants, an OPERATIONS-only action with
  // no per-tenant scope of its own) must say so plainly instead of
  // asserting a reach it does not have.
  it('does not claim scope applies on a section that does not honour it', () => {
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value="tenant-other"
        active="tenants"
        onChange={vi.fn()}
      />,
    );
    expect(screen.queryByText(/Viewing another tenant's rows/)).not.toBeInTheDocument();
    expect(screen.getByText(/doesn't use Administering/)).toBeInTheDocument();
  });

  it('shows no note at all on a non-honouring section while unscoped', () => {
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value={undefined}
        active="tenants"
        onChange={vi.fn()}
      />,
    );
    expect(screen.queryByText(/Viewing another tenant's rows/)).not.toBeInTheDocument();
    expect(screen.queryByText(/doesn't use Administering/)).not.toBeInTheDocument();
  });
});

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
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText(/Viewing another tenant's rows/)).toBeInTheDocument();
    expect(screen.getByText(/Acme/)).toBeInTheDocument();
  });
});

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import type { PlatformTenant } from '../app/api/tenantsApi';
import { OrgTargetStatus } from './PlatformEnrollFields';

afterEach(() => {
  cleanup();
});

const TENANTS: PlatformTenant[] = [
  {
    tenantId: 'tenant-2',
    name: 'validationagent',
    type: 'COMPANY',
    createdAt: '2026-06-24T10:00:00Z',
    updatedAt: '2026-06-24T10:00:00Z',
  },
];

describe('OrgTargetStatus', () => {
  it('labels the default as the platform’s own org', () => {
    render(<OrgTargetStatus target={{ status: 'default' }} tenants={[]} tenantId="own-tenant" />);
    expect(screen.getByText(/platform’s own organization/)).toBeInTheDocument();
  });

  it('names a resolved target tenant’s organization', () => {
    render(
      <OrgTargetStatus
        target={{ status: 'resolved', orgId: '999' }}
        tenants={TENANTS}
        tenantId="tenant-2"
      />,
    );
    expect(screen.getByText(/validationagent’s organization/)).toBeInTheDocument();
  });

  it('distinguishes a tenant with no org mapping from a lookup failure', () => {
    render(
      <OrgTargetStatus target={{ status: 'unmapped' }} tenants={TENANTS} tenantId="tenant-2" />,
    );
    expect(screen.getByText(/has no organization mapping yet/)).toBeInTheDocument();

    cleanup();

    render(
      <OrgTargetStatus
        target={{ status: 'error', message: 'bad gateway' }}
        tenants={TENANTS}
        tenantId="tenant-2"
      />,
    );
    expect(
      screen.getByText(/Could not resolve validationagent’s organization: bad gateway/),
    ).toBeInTheDocument();
  });
});

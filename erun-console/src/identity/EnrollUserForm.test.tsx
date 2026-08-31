import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { EnrollUserForm } from './EnrollUserForm';

interface MockReq {
  method: string;
  url: string;
  body?: unknown;
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
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      };
      calls.push(req);
      return Promise.resolve(handler(req));
    }),
  );
  return calls;
}

const TENANTS = [
  {
    tenantId: 'own-tenant',
    name: 'operations-hq',
    type: 'OPERATIONS',
    createdAt: '2026-06-24T10:00:00Z',
    updatedAt: '2026-06-24T10:00:00Z',
    userCount: 4,
  },
  {
    tenantId: 'other-tenant',
    name: 'validationagent',
    type: 'COMPANY',
    createdAt: '2026-06-24T10:00:00Z',
    updatedAt: '2026-06-24T10:00:00Z',
    userCount: 0,
  },
];

function mockTenantsAndUsers(onEnroll: (req: MockReq) => Response | undefined): void {
  mockFetch((req) => {
    if (req.url === '/v1/tenants' && req.method === 'GET') {
      return jsonResponse(TENANTS);
    }
    if (req.url === '/v1/users' && req.method === 'POST') {
      return onEnroll(req) ?? jsonResponse({}, 500);
    }
    return jsonResponse({}, 404);
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('EnrollUserForm', () => {
  it('does not show a tenant selector for a non-OPERATIONS caller', async () => {
    mockTenantsAndUsers(() => undefined);
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="own-tenant" tenantType="COMPANY" />,
    );
    await screen.findByText('Enroll a user directly');

    expect(screen.queryByLabelText('Tenant')).not.toBeInTheDocument();
  });

  it('shows the tenant selector for an OPERATIONS caller', async () => {
    mockTenantsAndUsers(() => undefined);
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );

    expect(await screen.findByLabelText('Tenant')).toBeInTheDocument();
  });

  it('defaults the target to the signed-in tenant and omits tenantId when unchanged', async () => {
    let enrollBody: unknown;
    mockTenantsAndUsers((req) => {
      enrollBody = req.body;
      return jsonResponse(
        { userId: 'u-1', tenantId: 'own-tenant', username: 'alice', alreadyEnrolled: false },
        201,
      );
    });
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByLabelText('Tenant');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'alice' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll directly' }));

    expect(await screen.findByText(/Enrolled alice/)).toBeInTheDocument();
    expect(enrollBody).toEqual({ username: 'alice' });
  });

  it('states the first user of a tenant with no users will receive TenantAdmin, before submitting', async () => {
    mockTenantsAndUsers(() => undefined);
    // The target defaults to ownTenantId, and 'other-tenant' is seeded with
    // userCount: 0 above -- exercising the default-target path (rather than
    // driving the Radix Select popover, which jsdom does not render
    // interactively) is enough to prove the notice reacts to the resolved
    // target tenant's own count.
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="other-tenant" tenantType="OPERATIONS" />,
    );

    expect(await screen.findByText(/will be granted TenantAdmin/)).toBeInTheDocument();
  });

  it('reports a no-op re-enrollment as already enrolled, distinctly from a username collision', async () => {
    mockTenantsAndUsers(() =>
      jsonResponse(
        { userId: 'u-1', tenantId: 'own-tenant', username: 'alice', alreadyEnrolled: true },
        200,
      ),
    );
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByLabelText('Tenant');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'alice' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll directly' }));

    expect(await screen.findByText(/already enrolled/)).toBeInTheDocument();
    expect(screen.queryByText(/username already exists/)).not.toBeInTheDocument();
  });

  it('reports a genuine username collision distinctly from an already-enrolled no-op', async () => {
    mockTenantsAndUsers(() =>
      jsonResponse(
        {
          code: 'USERNAME_TAKEN',
          message: 'a user with this username already exists in the target tenant',
        },
        409,
      ),
    );
    renderWithStore(
      <EnrollUserForm token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByLabelText('Tenant');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'alice' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll directly' }));

    expect(await screen.findByText(/username already exists/)).toBeInTheDocument();
    expect(screen.queryByText(/already enrolled/)).not.toBeInTheDocument();
  });
});

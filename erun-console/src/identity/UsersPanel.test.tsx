import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { UsersPanel } from './UsersPanel';

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

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('UsersPanel', () => {
  it('lists identities and renders an empty state when there are none', async () => {
    mockFetch(() => jsonResponse([]));
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    expect(await screen.findByText('No users enrolled yet.')).toBeInTheDocument();
  });

  it('renders users returned by GET /v1/identity/users', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          {
            id: 'idp-1',
            username: 'alice',
            state: 'USER_STATE_ACTIVE',
            email: 'alice@example.com',
          },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    expect(await screen.findByText('alice')).toBeInTheDocument();
    expect(screen.getByText('alice@example.com')).toBeInTheDocument();
    expect(screen.getByText('USER_STATE_ACTIVE')).toBeInTheDocument();
  });

  it('enrolls a user and reports the erun mapping, mail path: invite email', async () => {
    let listedAfterEnroll = false;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users') {
        return jsonResponse({
          idpUser: { id: 'idp-2', username: 'bob', state: 'USER_STATE_INITIAL' },
          erunUser: { userId: 'erun-2', username: 'bob' },
          mailDeliveryConfigured: true,
        });
      }
      if (req.url === '/v1/identity/users') {
        listedAfterEnroll = true;
        return jsonResponse([]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('No users enrolled yet.');

    const form = within(screen.getByRole('form', { name: 'Enroll a user' }));
    fireEvent.change(form.getByLabelText('Username', { exact: false }), {
      target: { value: 'bob' },
    });
    fireEvent.change(form.getByLabelText('Email', { exact: false }), {
      target: { value: 'bob@example.com' },
    });
    fireEvent.click(form.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/Enrolled bob/)).toBeInTheDocument();
    expect(screen.getByText(/An invite email is on its way/)).toBeInTheDocument();
    expect(listedAfterEnroll).toBe(true);
    // The invite-email path never mints a credential to show.
    expect(screen.queryByText(/Temporary password/)).not.toBeInTheDocument();
  });

  // erun#1756's default-path guarantee: leaving the target tenant untouched
  // must send the exact same request body as before orgId existed, and must
  // never resolve an org at all — a network call the caller never asked for
  // is itself a defect, not just an unused result.
  it('omits orgId and never resolves an org when the target tenant is unchanged', async () => {
    let enrollBody: unknown;
    let tenantIssuersCalled = false;
    const calls = mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users') {
        enrollBody = req.body;
        return jsonResponse({
          idpUser: { id: 'idp-5', username: 'erin', state: 'USER_STATE_INITIAL' },
          erunUser: { userId: 'erun-5', username: 'erin' },
          mailDeliveryConfigured: true,
        });
      }
      if (req.url.startsWith('/v1/tenant-issuers')) {
        tenantIssuersCalled = true;
      }
      return jsonResponse([]);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('No users enrolled yet.');

    expect(
      screen.getByText('Creates the identity in the platform’s own organization.'),
    ).toBeInTheDocument();

    const form = within(screen.getByRole('form', { name: 'Enroll a user' }));
    fireEvent.change(form.getByLabelText('Username', { exact: false }), {
      target: { value: 'erin' },
    });
    fireEvent.change(form.getByLabelText('Email', { exact: false }), {
      target: { value: 'erin@example.com' },
    });
    fireEvent.click(form.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/Enrolled erin/)).toBeInTheDocument();
    expect(enrollBody).toEqual({ username: 'erin', email: 'erin@example.com' });
    expect(tenantIssuersCalled).toBe(false);
    expect(calls.some((call) => call.url.startsWith('/v1/tenant-issuers'))).toBe(false);
  });

  it('shows a one-time, copyable temporary password when mail is not configured', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users') {
        return jsonResponse({
          idpUser: { id: 'idp-4', username: 'dana', state: 'USER_STATE_ACTIVE' },
          erunUser: { userId: 'erun-4', username: 'dana' },
          mailDeliveryConfigured: false,
          temporaryPassword: 'Er7hK2mQ9xL4nP6z!',
          warning: "This platform's identity provider has no SMTP configured.",
        });
      }
      return jsonResponse([]);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('No users enrolled yet.');

    const form = within(screen.getByRole('form', { name: 'Enroll a user' }));
    fireEvent.change(form.getByLabelText('Username', { exact: false }), {
      target: { value: 'dana' },
    });
    fireEvent.change(form.getByLabelText('Email', { exact: false }), {
      target: { value: 'dana@example.com' },
    });
    fireEvent.click(form.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/Outbound mail is not configured/)).toBeInTheDocument();
    expect(screen.getByText('Temporary password for dana')).toBeInTheDocument();
    const passwordField = screen.getByLabelText<HTMLInputElement>('Temporary password');
    expect(passwordField.value).toBe('Er7hK2mQ9xL4nP6z!');
    expect(screen.getByText(/shown once and will not be shown again/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith('Er7hK2mQ9xL4nP6z!');
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText('Temporary password for dana')).not.toBeInTheDocument();
    // The enrolled-user status message stays; only the one-time credential
    // itself is gone from the controller's state.
    expect(screen.getByText(/Enrolled dana/)).toBeInTheDocument();
  });

  it('reports the orphaned IdP identity when the erun mapping fails', async () => {
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users') {
        return jsonResponse({
          idpUser: { id: 'idp-3', username: 'carol', state: 'USER_STATE_INITIAL' },
          error: 'identity created in the identity provider but the erun user mapping failed',
        });
      }
      return jsonResponse([]);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('No users enrolled yet.');

    const form = within(screen.getByRole('form', { name: 'Enroll a user' }));
    fireEvent.change(form.getByLabelText('Username', { exact: false }), {
      target: { value: 'carol' },
    });
    fireEvent.change(form.getByLabelText('Email', { exact: false }), {
      target: { value: 'carol@example.com' },
    });
    fireEvent.click(form.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/could not be enrolled as an erun user/)).toBeInTheDocument();
  });

  it('distinguishes a tenant member from a self-registered IdP-only account', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          { id: 'idp-1', username: 'alice', state: 'USER_STATE_ACTIVE', enrolled: true },
          { id: 'idp-2', username: 'stranger', state: 'USER_STATE_ACTIVE', enrolled: false },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('alice');

    expect(screen.getByText('Tenant member')).toBeInTheDocument();
    expect(screen.getByText('IdP only, not enrolled')).toBeInTheDocument();
  });

  it('does not offer Deactivate on the platform own machine accounts', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          { id: 'svc-1', username: 'admin-sa', state: 'USER_STATE_ACTIVE', isMachine: true },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('admin-sa');

    expect(screen.getByText('Machine account')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Deactivate' })).not.toBeInTheDocument();
  });

  it('does not deactivate a user on a single click', async () => {
    let deactivateCalled = false;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users/idp-1/deactivate') {
        deactivateCalled = true;
        return jsonResponse({});
      }
      if (req.url === '/v1/identity/users') {
        return jsonResponse([{ id: 'idp-1', username: 'alice', state: 'USER_STATE_ACTIVE' }]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    expect(deactivateCalled).toBe(false);
    expect(screen.getByText('USER_STATE_ACTIVE')).toBeInTheDocument();
  });

  it('names the user and the consequence in the deactivate confirmation', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([{ id: 'idp-1', username: 'alice', state: 'USER_STATE_ACTIVE' }]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('heading', { name: /alice/ })).toBeInTheDocument();
    expect(within(dialog).getByText(/next sign-in/)).toBeInTheDocument();
  });

  it('leaves the user active when the deactivate confirmation is cancelled', async () => {
    let deactivateCalled = false;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users/idp-1/deactivate') {
        deactivateCalled = true;
        return jsonResponse({});
      }
      if (req.url === '/v1/identity/users') {
        return jsonResponse([{ id: 'idp-1', username: 'alice', state: 'USER_STATE_ACTIVE' }]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(deactivateCalled).toBe(false);
    expect(screen.getByText('USER_STATE_ACTIVE')).toBeInTheDocument();
  });

  it('disables cancel and confirm while the deactivate request is in flight', async () => {
    let resolveDeactivate: (() => void) | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL, init?: RequestInit) => {
        const url = requestUrl(input);
        const method = init?.method ?? 'GET';
        if (method === 'POST' && url === '/v1/identity/users/idp-1/deactivate') {
          return new Promise<Response>((resolve) => {
            resolveDeactivate = () => {
              resolve(jsonResponse({}));
            };
          });
        }
        if (url === '/v1/identity/users') {
          return Promise.resolve(
            jsonResponse([{ id: 'idp-1', username: 'alice', state: 'USER_STATE_ACTIVE' }]),
          );
        }
        return Promise.resolve(jsonResponse({}, 404));
      }),
    );

    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Deactivate' }));

    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(within(dialog).getByRole('button', { name: /Deactivate/ })).toBeDisabled();

    resolveDeactivate?.();
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('deactivates an active user through the confirmation dialog and refreshes the list', async () => {
    let deactivateCalled = false;
    let listCount = 0;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users/idp-1/deactivate') {
        deactivateCalled = true;
        return jsonResponse({});
      }
      if (req.url === '/v1/identity/users') {
        listCount += 1;
        const state = listCount === 1 ? 'USER_STATE_ACTIVE' : 'USER_STATE_INACTIVE';
        return jsonResponse([{ id: 'idp-1', username: 'alice', state }]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Deactivate' }));

    await screen.findByText('USER_STATE_INACTIVE');
    expect(deactivateCalled).toBe(true);
  });

  it('offers Manage roles only for an enrolled user with an erun mapping', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          {
            id: 'idp-1',
            username: 'alice',
            state: 'USER_STATE_ACTIVE',
            enrolled: true,
            erunUserId: 'erun-1',
          },
          { id: 'idp-2', username: 'stranger', state: 'USER_STATE_ACTIVE', enrolled: false },
          { id: 'svc-1', username: 'admin-sa', state: 'USER_STATE_ACTIVE', isMachine: true },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('alice');

    expect(screen.getAllByRole('button', { name: 'Manage roles' })).toHaveLength(1);
  });

  it('opens the roles dialog and grants a listed role to the user', async () => {
    let grantBody: unknown;
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          {
            id: 'idp-1',
            username: 'alice',
            state: 'USER_STATE_ACTIVE',
            enrolled: true,
            erunUserId: 'erun-1',
          },
        ]);
      }
      if (req.url === '/v1/roles') {
        return jsonResponse([
          { roleId: 'role-read', tenantId: 't1', name: 'ReadAll', permissions: [] },
          { roleId: 'role-write', tenantId: 't1', name: 'WriteAll', permissions: [] },
        ]);
      }
      if (req.url === '/v1/users/erun-1/roles' && req.method === 'GET') {
        return jsonResponse([]);
      }
      if (req.url === '/v1/users/erun-1/roles' && req.method === 'POST') {
        grantBody = req.body;
        return jsonResponse({ tenantId: 't1', userId: 'erun-1', roleId: 'role-read' }, 201);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('alice');

    fireEvent.click(screen.getByRole('button', { name: 'Manage roles' }));
    await screen.findByText('Roles for alice');
    await screen.findByText('This user holds no roles yet.');

    fireEvent.click(screen.getByRole('combobox', { name: 'Grant a role' }));
    fireEvent.click(await screen.findByRole('option', { name: 'ReadAll' }));
    fireEvent.click(screen.getByRole('button', { name: 'Grant' }));

    await waitFor(() => {
      expect(grantBody).toEqual({ roleId: 'role-read' });
    });
  });

  it('surfaces the lockout-guard refusal when revoking the last grant-capable role', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/identity/users') {
        return jsonResponse([
          {
            id: 'idp-1',
            username: 'alice',
            state: 'USER_STATE_ACTIVE',
            enrolled: true,
            erunUserId: 'erun-1',
          },
        ]);
      }
      if (req.url === '/v1/roles') {
        return jsonResponse([
          { roleId: 'role-write', tenantId: 't1', name: 'WriteAll', permissions: [] },
        ]);
      }
      if (req.url === '/v1/users/erun-1/roles' && req.method === 'GET') {
        return jsonResponse([{ roleId: 'role-write', tenantId: 't1', name: 'WriteAll' }]);
      }
      if (req.url === '/v1/users/erun-1/roles/role-write' && req.method === 'DELETE') {
        return jsonResponse(
          { message: 'revoking this role would leave the tenant with no user able to grant roles' },
          409,
        );
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('alice');

    fireEvent.click(screen.getByRole('button', { name: 'Manage roles' }));
    await screen.findByText('Roles for alice');
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      /revoking this role would leave the tenant with no user able to grant roles/,
    );
    // The refused revoke must leave the role in place, not optimistically clear it.
    expect(screen.getByText('WriteAll')).toBeInTheDocument();
  });

  it('reactivates an inactive user on a single click, with no confirmation gate', async () => {
    let reactivateCalled = false;
    let listCount = 0;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/identity/users/idp-1/reactivate') {
        reactivateCalled = true;
        return jsonResponse({});
      }
      if (req.url === '/v1/identity/users') {
        listCount += 1;
        const state = listCount === 1 ? 'USER_STATE_INACTIVE' : 'USER_STATE_ACTIVE';
        return jsonResponse([{ id: 'idp-1', username: 'alice', state }]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(
      <UsersPanel token="dev-token" ownTenantId="own-tenant" tenantType="OPERATIONS" />,
    );
    await screen.findByText('USER_STATE_INACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Reactivate' }));

    await screen.findByText('USER_STATE_ACTIVE');
    expect(reactivateCalled).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

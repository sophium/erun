import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { TenantsPanel } from './TenantsPanel';

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

function fillRequiredFields(name: string, issuer: string): void {
  // Anchored regexes: a plain substring match on "Name" would also hit the
  // "Display name (optional)" label further down the form.
  fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: name } });
  fireEvent.change(screen.getByLabelText(/^Issuer/), { target: { value: issuer } });
}

describe('TenantsPanel', () => {
  it('lists tenants and renders an empty state when there are none', async () => {
    mockFetch(() => jsonResponse([]));
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    expect(await screen.findByText('No tenants registered yet.')).toBeInTheDocument();
  });

  it('renders tenants returned by GET /v1/tenants', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/tenants' && req.method === 'GET') {
        return jsonResponse([
          {
            tenantId: 'tn-1',
            name: 'acme',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
          },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    expect(await screen.findByText('acme')).toBeInTheDocument();
    // Scoped to a table cell: the create form's own Type select also defaults
    // to displaying "COMPANY", so a bare getByText would be ambiguous.
    expect(screen.getByRole('cell', { name: 'COMPANY' })).toBeInTheDocument();
    // The response carried no userCount at all (a read path that never
    // counts) -- this must render as "unresolved", never as the same "0
    // users" badge a genuinely empty tenant gets.
    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(screen.queryByText('No users')).not.toBeInTheDocument();
  });

  it('flags a tenant with zero users as inert, distinctly from one with an unresolved count', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/tenants' && req.method === 'GET') {
        return jsonResponse([
          {
            tenantId: 'tn-empty',
            name: 'validationagent',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
            userCount: 0,
          },
          {
            tenantId: 'tn-populated',
            name: 'acme',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
            userCount: 3,
          },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('validationagent');

    expect(screen.getByText('No users')).toBeInTheDocument();
    expect(screen.getByText('3 users')).toBeInTheDocument();
    expect(screen.queryByText('Unknown')).not.toBeInTheDocument();
  });

  it('registers a tenant and reports the no-first-user note, then refreshes the list', async () => {
    let listedAfterCreate = false;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/tenants') {
        return jsonResponse(
          {
            tenantId: 'tn-2',
            name: 'globex',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
          },
          201,
        );
      }
      if (req.url === '/v1/tenants') {
        listedAfterCreate = true;
        return jsonResponse([]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('No tenants registered yet.');

    fillRequiredFields('globex', 'https://idp.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Register tenant' }));

    expect(await screen.findByText(/Registered globex/)).toBeInTheDocument();
    expect(screen.getByText(/No first user is created here/)).toBeInTheDocument();
    expect(listedAfterCreate).toBe(true);
  });

  it('renders a name-validation error against the Name field, not as a bare status', async () => {
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/tenants') {
        return jsonResponse(
          {
            code: 'BAD_REQUEST',
            message:
              'invalid tenant name "ac-me": tenant names must use only lowercase letters and digits with no hyphens, so the <tenant>-<env> namespace is unambiguous',
          },
          400,
        );
      }
      return jsonResponse([]);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('No tenants registered yet.');

    fillRequiredFields('ac-me', 'https://idp.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Register tenant' }));

    expect(await screen.findByText(/no hyphens/)).toBeInTheDocument();
    // Not rendered as the generic "create tenant failed (400)" banner.
    expect(screen.queryByText(/Could not register tenant/)).not.toBeInTheDocument();
  });

  it('renders an issuer-conflict error against the Issuer field', async () => {
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/tenants') {
        return jsonResponse(
          {
            code: 'CONFLICT',
            message: 'issuer "https://idp.example.com" is already mapped for org value "42"',
          },
          409,
        );
      }
      return jsonResponse([]);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('No tenants registered yet.');

    fillRequiredFields('acme', 'https://idp.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Register tenant' }));

    expect(await screen.findByText(/already mapped for org value/)).toBeInTheDocument();
    expect(screen.queryByText(/Could not register tenant/)).not.toBeInTheDocument();
  });

  it('renders a type-validation error against the Type field', async () => {
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/tenants') {
        return jsonResponse(
          { code: 'BAD_REQUEST', message: 'type must be one of COMPANY, OPERATIONS' },
          400,
        );
      }
      return jsonResponse([]);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('No tenants registered yet.');

    fillRequiredFields('acme', 'https://idp.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Register tenant' }));

    expect(await screen.findByText('type must be one of COMPANY, OPERATIONS')).toBeInTheDocument();
    expect(screen.queryByText(/Could not register tenant/)).not.toBeInTheDocument();
  });

  it('renders a general banner for an error that names no single field', async () => {
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/tenants') {
        return jsonResponse({ code: 'BAD_REQUEST', message: 'invalid request body' }, 400);
      }
      return jsonResponse([]);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('No tenants registered yet.');

    fillRequiredFields('acme', 'https://idp.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Register tenant' }));

    expect(
      await screen.findByText('Could not register tenant: invalid request body'),
    ).toBeInTheDocument();
  });

  it('sets a tenant quota through the per-row dialog', async () => {
    let putBody: unknown;
    mockFetch((req) => {
      if (req.url === '/v1/tenants' && req.method === 'GET') {
        return jsonResponse([
          {
            tenantId: 'tn-1',
            name: 'acme',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
          },
        ]);
      }
      if (req.url === '/v1/tenants/tn-1/quota' && req.method === 'PUT') {
        putBody = req.body;
        return jsonResponse({ tenantId: 'tn-1', ...(req.body as object) });
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('acme');

    fireEvent.click(screen.getByRole('button', { name: 'Set quota' }));
    expect(screen.getByText('Set quota for acme')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/^Environments/), { target: { value: '5' } });
    fireEvent.change(screen.getByLabelText(/^Per-environment CPU/), { target: { value: '500' } });
    fireEvent.change(screen.getByLabelText(/^Per-environment memory/), {
      target: { value: '512' },
    });
    fireEvent.change(screen.getByLabelText(/^Per-environment storage/), {
      target: { value: '10' },
    });
    fireEvent.change(screen.getByLabelText(/^Total CPU/), { target: { value: '2000' } });
    fireEvent.change(screen.getByLabelText(/^Total memory/), { target: { value: '4096' } });
    fireEvent.change(screen.getByLabelText(/^Total storage/), { target: { value: '100' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save quota' }));

    expect(await screen.findByText('Quota updated.')).toBeInTheDocument();
    expect(putBody).toEqual({
      maxEnvironments: 5,
      maxCpuMillicores: 500,
      maxMemoryMb: 512,
      maxStorageGb: 10,
      maxTotalCpuMillicores: 2000,
      maxTotalMemoryMb: 4096,
      maxTotalStorageGb: 100,
    });
  });

  it('enrolls the first user of an empty tenant from the Tenants view and names the TenantAdmin grant', async () => {
    let enrollBody: unknown;
    mockFetch((req) => {
      if (req.url === '/v1/tenants' && req.method === 'GET') {
        return jsonResponse([
          {
            tenantId: 'tn-empty',
            name: 'validationagent',
            type: 'COMPANY',
            createdAt: '2026-06-24T10:00:00Z',
            updatedAt: '2026-06-24T10:00:00Z',
            userCount: 0,
          },
        ]);
      }
      if (req.url === '/v1/users' && req.method === 'POST') {
        enrollBody = req.body;
        return jsonResponse(
          { userId: 'u-1', tenantId: 'tn-empty', username: 'jane', alreadyEnrolled: false },
          201,
        );
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<TenantsPanel token="dev-token" docsUrl={undefined} />);
    await screen.findByText('validationagent');

    fireEvent.click(screen.getByRole('button', { name: 'Enroll user' }));
    const dialog = await screen.findByRole('dialog');
    // Told before confirming, not discovered after (erun#1744 acceptance
    // criterion): the notice is visible as soon as the dialog opens.
    expect(within(dialog).getByText(/will be granted TenantAdmin/)).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('Username', { exact: false }), {
      target: { value: 'jane' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Enroll user' }));

    expect(
      await screen.findByText(/first user and have been granted TenantAdmin/),
    ).toBeInTheDocument();
    expect(enrollBody).toEqual({ username: 'jane', tenantId: 'tn-empty' });
  });

  it('links the org-scoped-issuer explanation using the instance docs URL when provided', async () => {
    mockFetch(() => jsonResponse([]));
    renderWithStore(<TenantsPanel token="dev-token" docsUrl="https://docs.acme.example" />);
    await screen.findByText('No tenants registered yet.');

    const link = screen.getByRole('link', { name: 'how tenant resolution works' });
    expect(link).toHaveAttribute(
      'href',
      'https://docs.acme.example/agent-reference/api-protocol#tenant-issuers',
    );
  });
});

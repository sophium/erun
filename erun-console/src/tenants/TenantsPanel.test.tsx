import { cleanup, fireEvent, screen } from '@testing-library/react';
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

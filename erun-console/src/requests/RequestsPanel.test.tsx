import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { RequestsPanel } from './RequestsPanel';

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

const PENDING_REQUEST = {
  inviteRequestId: 'ir-1',
  issuer: 'https://idp.example.com',
  subject: 'sub-1',
  email: 'newcomer@example.com',
  kind: 'JOIN_TENANT',
  tenantName: 'acme',
  note: 'I already have a local tenant and environment running.',
  status: 'PENDING',
  createdAt: '2026-06-24T10:00:00Z',
  updatedAt: '2026-06-24T10:00:00Z',
};

const FULL_CAPABILITIES = [
  { method: 'POST', path: '/v1/invite-requests/{invite_request_id}/approve' },
  { method: 'POST', path: '/v1/invite-requests/{invite_request_id}/decline' },
];

function whoami(capabilities: unknown): Record<string, unknown> {
  return {
    tenantId: 'tn-1',
    userId: 'user-1',
    issuer: 'https://idp.example.com',
    subject: 'admin-1',
    capabilities,
  };
}

function renderPanel(rateLimitWindowSeconds = 60): void {
  renderWithStore(
    <RequestsPanel
      token="dev-token"
      tenantType="COMPANY"
      rateLimitWindowSeconds={rateLimitWindowSeconds}
    />,
  );
}

describe('RequestsPanel', () => {
  it('renders an empty state when there are no pending requests', async () => {
    mockFetch((req) => {
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse([]);
      }
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami(FULL_CAPABILITIES));
      }
      return jsonResponse({}, 404);
    });
    renderPanel();
    expect(await screen.findByText('No pending requests.')).toBeInTheDocument();
  });

  it('lists a pending request with its requester, kind, note, and actions', async () => {
    mockFetch((req) => {
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse([PENDING_REQUEST]);
      }
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami(FULL_CAPABILITIES));
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    expect(await screen.findByText('Join acme')).toBeInTheDocument();
    expect(screen.getByText(/sub-1/)).toBeInTheDocument();
    expect(screen.getByText(/newcomer@example.com/)).toBeInTheDocument();
    expect(
      screen.getByText('I already have a local tenant and environment running.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Issue invitation/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeInTheDocument();
  });

  it('issues an invitation, shows the minted link, and the row disappears once the list refetches', async () => {
    let approved = false;
    mockFetch((req) => {
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami(FULL_CAPABILITIES));
      }
      if (req.method === 'POST' && req.url === '/v1/invite-requests/ir-1/approve') {
        approved = true;
        return jsonResponse({
          ...PENDING_REQUEST,
          status: 'APPROVED',
          mintedInviteId: 'inv-1',
          mintedInviteToken: 'tok-abc',
          mintedInviteExpiresAt: '2026-07-01T00:00:00Z',
        });
      }
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse(approved ? [] : [PENDING_REQUEST]);
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    fireEvent.click(await screen.findByRole('button', { name: /Issue invitation/ }));

    expect(await screen.findByText('Invitation issued')).toBeInTheDocument();
    expect(screen.getByDisplayValue(/tok-abc/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => {
      expect(screen.getByText('No pending requests.')).toBeInTheDocument();
    });
  });

  it('requires a non-empty reason before the decline confirm button enables, and submits it', async () => {
    let declineBody: unknown;
    mockFetch((req) => {
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami(FULL_CAPABILITIES));
      }
      if (req.method === 'POST' && req.url === '/v1/invite-requests/ir-1/decline') {
        declineBody = req.body;
        return jsonResponse({ ...PENDING_REQUEST, status: 'DECLINED', declineReason: 'no room' });
      }
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse(declineBody === undefined ? [PENDING_REQUEST] : []);
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    fireEvent.click(await screen.findByRole('button', { name: 'Decline' }));
    // The dialog's own confirm button is also named "Decline" -- the row's
    // button stays mounted behind the open dialog, so every lookup below is
    // scoped to the dialog to disambiguate.
    const dialog = screen.getByRole('dialog');
    const dialogConfirm = within(dialog).getByRole('button', { name: 'Decline' });
    expect(dialogConfirm).toBeDisabled();

    fireEvent.change(within(dialog).getByLabelText(/^Reason/), {
      target: { value: 'no room' },
    });
    expect(dialogConfirm).not.toBeDisabled();

    fireEvent.click(dialogConfirm);
    await waitFor(() => {
      expect(declineBody).toEqual({ reason: 'no room' });
    });
  });

  it('hides Issue invitation/Decline and names the missing access when capabilities refuse both', async () => {
    mockFetch((req) => {
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse([PENDING_REQUEST]);
      }
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami([]));
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    expect(await screen.findByText('Join acme')).toBeInTheDocument();
    expect(
      await screen.findByText(/You do not have permission to issue invitations or decline/),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Issue invitation/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Decline' })).not.toBeInTheDocument();
  });

  it('keeps Issue invitation/Decline attemptable when whoami reports no capability set at all (null)', async () => {
    // capabilities: null means the platform could not resolve a set, not
    // that it resolved to "nothing" -- distinct from the [] case above,
    // which is a real, known refusal of both actions.
    mockFetch((req) => {
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse([PENDING_REQUEST]);
      }
      if (req.url === '/v1/whoami') {
        return jsonResponse(whoami(null));
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    expect(await screen.findByText('Join acme')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Issue invitation/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeInTheDocument();
    expect(
      screen.queryByText(/You do not have permission to issue invitations or decline/),
    ).not.toBeInTheDocument();
  });

  it('reports a whoami failure and still leaves Issue invitation/Decline attemptable', async () => {
    mockFetch((req) => {
      if (req.url.startsWith('/v1/invite-requests')) {
        return jsonResponse([PENDING_REQUEST]);
      }
      if (req.url === '/v1/whoami') {
        return jsonResponse({ message: 'internal error' }, 500);
      }
      return jsonResponse({}, 404);
    });
    renderPanel();

    expect(await screen.findByText('Join acme')).toBeInTheDocument();
    expect(
      await screen.findByText(/Could not check your permissions for this queue/),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Issue invitation/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeInTheDocument();
    expect(
      screen.queryByText(/You do not have permission to issue invitations or decline/),
    ).not.toBeInTheDocument();
  });

  it('shows the rate-limit editor for an OPERATIONS tenant', async () => {
    mockFetch(() => jsonResponse([]));
    renderWithStore(
      <RequestsPanel token="dev-token" tenantType="OPERATIONS" rateLimitWindowSeconds={60} />,
    );
    expect(await screen.findByText('No pending requests.')).toBeInTheDocument();
    expect(screen.getByText('Invite-request rate limit')).toBeInTheDocument();
  });

  it('hides the rate-limit editor for a non-OPERATIONS tenant', async () => {
    mockFetch(() => jsonResponse([]));
    renderPanel();
    expect(await screen.findByText('No pending requests.')).toBeInTheDocument();
    expect(screen.queryByText('Invite-request rate limit')).not.toBeInTheDocument();
  });
});

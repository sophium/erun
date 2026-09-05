import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { InvitesPanel } from './InvitesPanel';

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

describe('InvitesPanel', () => {
  it('renders an empty state when there are no outstanding invites', async () => {
    mockFetch(() => jsonResponse([]));
    renderWithStore(<InvitesPanel token="dev-token" />);
    expect(await screen.findByText('No outstanding invites.')).toBeInTheDocument();
  });

  it('lists outstanding invites returned by GET /v1/invites', async () => {
    mockFetch((req) => {
      if (req.url === '/v1/invites') {
        return jsonResponse([
          {
            inviteId: 'invite-1',
            tenantId: 'tenant-1',
            token: 'tok-1',
            email: 'new@example.com',
            expiresAt: '2030-01-01T00:00:00Z',
          },
        ]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<InvitesPanel token="dev-token" />);
    expect(await screen.findByText('new@example.com')).toBeInTheDocument();
  });

  it('creates an invite and shows the copyable accept link', async () => {
    let listedAfterCreate = false;
    mockFetch((req) => {
      if (req.method === 'POST' && req.url === '/v1/invites') {
        return jsonResponse({
          inviteId: 'invite-2',
          tenantId: 'tenant-1',
          token: 'tok-abc123',
          email: 'bob@example.com',
          expiresAt: '2030-01-01T00:00:00Z',
        });
      }
      if (req.url === '/v1/invites') {
        listedAfterCreate = true;
        return jsonResponse([]);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<InvitesPanel token="dev-token" />);
    await screen.findByText('No outstanding invites.');

    fireEvent.change(screen.getByLabelText('Email (optional)'), {
      target: { value: 'bob@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));

    expect(await screen.findByText('Invite created')).toBeInTheDocument();
    const linkField = screen.getByLabelText<HTMLInputElement>('Invite link');
    expect(linkField.value).toContain('tok-abc123');
    expect(linkField.value).toContain('/accept-invite?token=');
    expect(listedAfterCreate).toBe(true);
  });

  it('revokes an invite and removes it from the list', async () => {
    let revokeCalled = false;
    let listCount = 0;
    mockFetch((req) => {
      if (req.method === 'DELETE' && req.url === '/v1/invites/invite-1') {
        revokeCalled = true;
        return jsonResponse({}, 204);
      }
      if (req.url === '/v1/invites') {
        listCount += 1;
        const invites =
          listCount === 1
            ? [
                {
                  inviteId: 'invite-1',
                  tenantId: 'tenant-1',
                  token: 'tok-1',
                  email: 'gone@example.com',
                  expiresAt: '2030-01-01T00:00:00Z',
                },
              ]
            : [];
        return jsonResponse(invites);
      }
      return jsonResponse({}, 404);
    });
    renderWithStore(<InvitesPanel token="dev-token" />);
    await screen.findByText('gone@example.com');

    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));

    await waitFor(() => {
      expect(revokeCalled).toBe(true);
    });
    await screen.findByText('No outstanding invites.');
  });
});

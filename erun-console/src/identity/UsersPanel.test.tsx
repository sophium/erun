import { cleanup, fireEvent, screen } from '@testing-library/react';
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
    renderWithStore(<UsersPanel token="dev-token" />);
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
    renderWithStore(<UsersPanel token="dev-token" />);
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
    renderWithStore(<UsersPanel token="dev-token" />);
    await screen.findByText('No users enrolled yet.');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'bob' },
    });
    fireEvent.change(screen.getByLabelText('Email', { exact: false }), {
      target: { value: 'bob@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/Enrolled bob/)).toBeInTheDocument();
    expect(screen.getByText(/An invite email is on its way/)).toBeInTheDocument();
    expect(listedAfterEnroll).toBe(true);
    // The invite-email path never mints a credential to show.
    expect(screen.queryByText(/Temporary password/)).not.toBeInTheDocument();
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
    renderWithStore(<UsersPanel token="dev-token" />);
    await screen.findByText('No users enrolled yet.');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'dana' },
    });
    fireEvent.change(screen.getByLabelText('Email', { exact: false }), {
      target: { value: 'dana@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll user' }));

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
    renderWithStore(<UsersPanel token="dev-token" />);
    await screen.findByText('No users enrolled yet.');

    fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
      target: { value: 'carol' },
    });
    fireEvent.change(screen.getByLabelText('Email', { exact: false }), {
      target: { value: 'carol@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Enroll user' }));

    expect(await screen.findByText(/could not be enrolled as an erun user/)).toBeInTheDocument();
  });

  it('deactivates an active user and refreshes the list', async () => {
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
    renderWithStore(<UsersPanel token="dev-token" />);
    await screen.findByText('USER_STATE_ACTIVE');

    fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    await screen.findByText('USER_STATE_INACTIVE');
    expect(deactivateCalled).toBe(true);
  });
});

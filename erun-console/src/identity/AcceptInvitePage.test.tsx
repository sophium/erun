import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { AcceptInvitePage } from './AcceptInvitePage';

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

function fillAndSubmit(username: string, password: string): void {
  fireEvent.change(screen.getByLabelText('Username', { exact: false }), {
    target: { value: username },
  });
  fireEvent.change(screen.getByLabelText('Password', { exact: false }), {
    target: { value: password },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
}

describe('AcceptInvitePage', () => {
  it('shows a clear message when the URL carries no token', () => {
    renderWithStore(<AcceptInvitePage token="" />);
    expect(screen.getByText(/missing its invite token/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create account' })).not.toBeInTheDocument();
  });

  it('creates the account and points the invitee at sign-in', async () => {
    const calls = mockFetch(() =>
      jsonResponse({
        idpUser: { id: 'idp-1', username: 'newbie', state: 'USER_STATE_ACTIVE' },
        erunUser: { userId: 'erun-1', username: 'newbie' },
      }),
    );
    renderWithStore(<AcceptInvitePage token="tok-abc" />);

    fillAndSubmit('newbie', 'S3cret!Pass');

    expect(await screen.findByText(/Your account is ready/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign in' })).toBeInTheDocument();
    const call = calls.find((c) => c.method === 'POST');
    expect(call?.url).toBe('/v1/invites/accept');
    expect(call?.body).toMatchObject({
      token: 'tok-abc',
      username: 'newbie',
      password: 'S3cret!Pass',
    });
  });

  it('reports an expired or already-used link plainly', async () => {
    mockFetch(() => jsonResponse('invite link has expired', 410));
    renderWithStore(<AcceptInvitePage token="tok-expired" />);

    fillAndSubmit('newbie', 'S3cret!Pass');

    expect(await screen.findByText(/expired or has already been used/)).toBeInTheDocument();
  });

  it('reports an invalid token plainly', async () => {
    mockFetch(() => jsonResponse('invite link is invalid', 404));
    renderWithStore(<AcceptInvitePage token="tok-bad" />);

    fillAndSubmit('newbie', 'S3cret!Pass');

    expect(await screen.findByText(/not valid/)).toBeInTheDocument();
  });

  it('reports a half-landed enrollment without claiming full success', async () => {
    mockFetch(() =>
      jsonResponse({
        idpUser: { id: 'idp-9', username: 'orphan', state: 'USER_STATE_ACTIVE' },
        error: 'identity created in the identity provider but the erun user mapping failed',
      }),
    );
    renderWithStore(<AcceptInvitePage token="tok-orphan" />);

    fillAndSubmit('orphan', 'S3cret!Pass');

    expect(await screen.findByText(/could not be finished/)).toBeInTheDocument();
    expect(screen.queryByText(/Your account is ready/)).not.toBeInTheDocument();
  });
});

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { SmtpSettingsPanel } from './SmtpSettingsPanel';

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

const NOT_CONFIGURED = { configured: false, config: {} };

const CONFIGURED = {
  configured: true,
  config: {
    host: 'smtp.example.com:587',
    user: 'erun',
    senderAddress: 'noreply@example.com',
    senderName: 'Erun Platform',
    replyToAddress: '',
    tls: true,
  },
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('SmtpSettingsPanel', () => {
  it('defaults to not configured on a fresh platform', async () => {
    mockFetch(() => jsonResponse(NOT_CONFIGURED));
    renderWithStore(<SmtpSettingsPanel token="dev-token" />);

    expect(await screen.findByText('Not configured')).toBeInTheDocument();
    expect(screen.getByLabelText<HTMLInputElement>('Host', { exact: false }).value).toBe('');
    expect(screen.getByLabelText('Password', { exact: false })).toHaveAttribute('required');
  });

  it('renders the current configuration split into host and port', async () => {
    mockFetch(() => jsonResponse(CONFIGURED));
    renderWithStore(<SmtpSettingsPanel token="dev-token" />);

    expect(await screen.findByText('Configured')).toBeInTheDocument();
    expect(screen.getByLabelText<HTMLInputElement>('Host', { exact: false }).value).toBe(
      'smtp.example.com',
    );
    expect(screen.getByLabelText<HTMLInputElement>('Port').value).toBe('587');
    expect(screen.getByLabelText<HTMLInputElement>('Username').value).toBe('erun');
    expect(screen.getByLabelText<HTMLInputElement>('From address', { exact: false }).value).toBe(
      'noreply@example.com',
    );
    // Once configured, the password is optional — the operator only supplies
    // it again to change it.
    expect(screen.getByLabelText('Password')).not.toHaveAttribute('required');
  });

  it('PATCHes the joined host:port and reports the saved state', async () => {
    const calls = mockFetch((req) => {
      if (req.method === 'PATCH') {
        return jsonResponse({
          configured: true,
          config: {
            host: 'smtp.newprovider.com:465',
            user: 'erun',
            senderAddress: 'noreply@example.com',
            senderName: '',
            replyToAddress: '',
            tls: true,
          },
        });
      }
      return jsonResponse(NOT_CONFIGURED);
    });
    renderWithStore(<SmtpSettingsPanel token="dev-token" />);
    await screen.findByText('Not configured');

    fireEvent.change(screen.getByLabelText('Host', { exact: false }), {
      target: { value: 'smtp.newprovider.com' },
    });
    fireEvent.change(screen.getByLabelText('Port'), { target: { value: '465' } });
    fireEvent.change(screen.getByLabelText('Password', { exact: false }), {
      target: { value: 'super-secret' },
    });
    fireEvent.change(screen.getByLabelText('From address', { exact: false }), {
      target: { value: 'noreply@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));

    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PATCH')).toBe(true);
    });
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/v1/identity/smtp-settings');
    expect(patch?.body).toMatchObject({
      host: 'smtp.newprovider.com:465',
      senderAddress: 'noreply@example.com',
      password: 'super-secret',
    });
    await screen.findByText('Configured');
  });

  it('reports the provider error from a failed save and keeps the form editable', async () => {
    mockFetch((req) => {
      if (req.method === 'PATCH') {
        return jsonResponse('smtp handshake failed: connection refused', 502);
      }
      return jsonResponse(NOT_CONFIGURED);
    });
    renderWithStore(<SmtpSettingsPanel token="dev-token" />);
    await screen.findByText('Not configured');

    fireEvent.change(screen.getByLabelText('Host', { exact: false }), {
      target: { value: 'smtp.example.com' },
    });
    fireEvent.change(screen.getByLabelText('Password', { exact: false }), {
      target: { value: 'wrong-secret' },
    });
    fireEvent.change(screen.getByLabelText('From address', { exact: false }), {
      target: { value: 'noreply@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));

    expect(await screen.findByText(/Could not save mail settings/)).toBeInTheDocument();
    // The form survives the failure — the operator's input is still there and
    // the Save button is still present to retry, not a dead-end error page.
    expect(screen.getByLabelText<HTMLInputElement>('Host', { exact: false }).value).toBe(
      'smtp.example.com',
    );
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeInTheDocument();
  });

  it('surfaces a load error', async () => {
    mockFetch(() => jsonResponse('forbidden', 403));
    renderWithStore(<SmtpSettingsPanel token="dev-token" />);
    expect(await screen.findByText(/Could not load mail settings/)).toBeInTheDocument();
  });
});

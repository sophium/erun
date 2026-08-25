import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithStore } from '../test/renderWithStore';
import { OrgSettingsPanel } from './OrgSettingsPanel';

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

const INITIAL_SETTINGS = {
  forceMfa: false,
  minPasswordLength: 8,
  passwordRequiresUppercase: true,
  passwordRequiresLowercase: true,
  passwordRequiresNumber: true,
  passwordRequiresSymbol: false,
  verifiedDomains: ['erun.example.com'],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('OrgSettingsPanel', () => {
  it('loads and renders the org settings and verified domains', async () => {
    mockFetch(() => jsonResponse(INITIAL_SETTINGS));
    renderWithStore(<OrgSettingsPanel token="dev-token" />);

    expect(await screen.findByText('erun.example.com')).toBeInTheDocument();
    expect(screen.getByLabelText<HTMLInputElement>('Minimum password length').value).toBe('8');
    expect(
      screen.getByRole('checkbox', { name: 'Require multi-factor authentication' }),
    ).toHaveAttribute('aria-checked', 'false');
  });

  it('PATCHes only the changed field and re-renders the saved settings', async () => {
    const calls = mockFetch((req) => {
      if (req.method === 'PATCH') {
        return jsonResponse({ ...INITIAL_SETTINGS, forceMfa: true });
      }
      return jsonResponse(INITIAL_SETTINGS);
    });
    renderWithStore(<OrgSettingsPanel token="dev-token" />);
    await screen.findByText('erun.example.com');

    fireEvent.click(screen.getByLabelText('Require multi-factor authentication'));
    fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));

    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PATCH')).toBe(true);
    });
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/v1/identity/org-settings');
    expect(patch?.body).toMatchObject({ forceMfa: true });
    await screen.findByRole('checkbox', { name: 'Require multi-factor authentication' });
  });

  it('surfaces a load error', async () => {
    mockFetch(() => jsonResponse('forbidden', 403));
    renderWithStore(<OrgSettingsPanel token="dev-token" />);
    expect(await screen.findByText(/Could not load org settings/)).toBeInTheDocument();
  });
});

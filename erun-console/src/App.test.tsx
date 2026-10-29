import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { beginTenantSwitch } from './shell/tenantSwitch';
import { renderWithStore } from './test/renderWithStore';

const PLATFORM = {
  issuer: 'https://auth.acme.example',
  apiUrl: 'https://console.acme.example',
  consoleUrl: 'https://console.acme.example',
  consoleClientId: 'console-client',
  cliClientId: 'cli-client',
  mobileClientId: 'mobile-client',
  brand: 'Acme',
  docsUrl: 'https://docs.acme.example',
  tagline: 'Acme, from idea to production.',
  logoUrl: '',
};

function jsonResponse(body: unknown): Response {
  return { ok: true, status: 200, json: () => Promise.resolve(body) } as unknown as Response;
}

beforeEach(() => {
  vi.stubEnv('VITE_DEV_BEARER_TOKEN', '');
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(jsonResponse(PLATFORM))),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  sessionStorage.clear();
  delete window.__ERUN_PLATFORM_BRAND__;
});

// The first paint uses the server-injected brand, so it never flips once discovery answers.
describe('App brand', () => {
  it('shows the server-supplied brand before discovery resolves and after', async () => {
    const brand = 'ErunPaaS';
    window.__ERUN_PLATFORM_BRAND__ = brand;
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ ...PLATFORM, brand }))),
    );

    renderWithStore(<App />);

    expect(document.title).toBe(`${brand} console`);
    expect(screen.getAllByText(brand).length).toBeGreaterThan(0);

    await screen.findByRole('heading', { level: 1, name: PLATFORM.tagline });

    expect(document.title).toBe(`${brand} console`);
    expect(screen.getAllByText(brand).length).toBeGreaterThan(0);
    expect(screen.queryByText('ERun console')).not.toBeInTheDocument();
  });
});

describe('App signed-out route', () => {
  // The signed-out screen needs real <h1>/<h2> headings inside a <main> landmark.
  it('is a real landing page, not merely a CenteredCard sign-in prompt', async () => {
    renderWithStore(<App />);

    await waitFor(() => {
      expect(screen.getAllByRole('heading').length).toBeGreaterThan(1);
    });
    expect(screen.getByRole('main')).toBeInTheDocument();
    const docsLinks = screen.getAllByRole('link', { name: 'Read the docs' });
    expect(docsLinks.length).toBeGreaterThan(0);
    for (const link of docsLinks) {
      expect(link).toHaveAttribute('href', 'https://docs.acme.example');
    }
  });

  it('renders the instance tagline from platform discovery as the h1', async () => {
    renderWithStore(<App />);

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Acme, from idea to production.' }),
    ).toBeInTheDocument();
  });

  it("renders the instance's own logo from platform discovery", async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({ ...PLATFORM, logoUrl: 'https://cdn.acme.example/logo.svg' }),
        ),
      ),
    );
    renderWithStore(<App />);

    await waitFor(() => {
      expect(document.querySelector('img')).toHaveAttribute(
        'src',
        'https://cdn.acme.example/logo.svg',
      );
    });
  });

  it('falls back to the bundled defaults when the platform sends none of the three', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({ ...PLATFORM, brand: '', docsUrl: '', tagline: '', logoUrl: '' }),
        ),
      ),
    );
    renderWithStore(<App />);

    // A half-configured instance falls back per field, never a blank hero or broken image.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: 'Agentic coding from idea to production, without compromising compliance.',
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Read the docs' })).toHaveAttribute(
      'href',
      'https://docs.erunpaas.com',
    );
    expect(document.querySelector('img')).toBeNull();
  });
});

function stubConfigFetch(tenant: { tenantId: string; name: string; type: string }): void {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL) => {
      const url = input instanceof URL ? input.href : input;
      if (url.includes('/v1/platform')) {
        return Promise.resolve(jsonResponse(PLATFORM));
      }
      if (url.includes('/v1/config')) {
        return Promise.resolve(jsonResponse({ tenant, environments: [], contexts: [] }));
      }
      return Promise.resolve(jsonResponse([]));
    }),
  );
}

// A switch is real only once the API resolves the new token to the requested tenant.
describe('App tenant switch mismatch', () => {
  it('surfaces a mismatch banner when a switch attempt resolves to a different tenant than requested', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    beginTenantSwitch({ tenantId: 'tenant-b', name: 'Beta' });
    stubConfigFetch({ tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' });

    renderWithStore(<App />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Beta');
    expect(alert).toHaveTextContent('Acme');
  });

  it('shows no mismatch banner when nothing was pending, or the switch reached its target', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    beginTenantSwitch({ tenantId: 'tenant-a', name: 'Acme' });
    stubConfigFetch({ tenantId: 'tenant-a', name: 'Acme', type: 'COMPANY' });

    renderWithStore(<App />);

    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});

// Sign-out from the not-enrolled card must return the whole app to sign-in.
describe('App not-enrolled route', () => {
  it('returns to the sign-in flow when the operator signs out from the not-enrolled card', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        const url = input instanceof URL ? input.href : input;
        if (url.includes('/v1/platform')) {
          return Promise.resolve(jsonResponse(PLATFORM));
        }
        if (url.includes('/v1/config')) {
          return Promise.resolve({
            ok: false,
            status: 401,
            json: () => Promise.resolve({}),
          } as unknown as Response);
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderWithStore(<App />);

    await screen.findByText('Not yet part of a tenant');
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));

    expect(
      await screen.findByRole('heading', { level: 2, name: 'What makes Acme different' }),
    ).toBeInTheDocument();
  });
});

// TENANT_UNRESOLVED must not render the not-enrolled advice; the caller may be enrolled.
describe('App tenant-unresolved route', () => {
  it('shows the tenant-unresolved card, not the not-enrolled card, for a TENANT_UNRESOLVED 401', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        const url = input instanceof URL ? input.href : input;
        if (url.includes('/v1/platform')) {
          return Promise.resolve(jsonResponse(PLATFORM));
        }
        if (url.includes('/v1/config')) {
          return Promise.resolve({
            ok: false,
            status: 401,
            json: () =>
              Promise.resolve({
                code: 'TENANT_UNRESOLVED',
                message:
                  'issuer "https://auth.acme.example" is org-scoped but the token carries no matching claim',
              }),
          } as unknown as Response);
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderWithStore(<App />);

    await screen.findByText('Could not determine your tenant');
    expect(screen.getByText(/carries no matching claim/)).toBeInTheDocument();
    expect(screen.queryByText('Not yet part of a tenant')).not.toBeInTheDocument();
  });
});

// RESOLUTION_FAILED gets its own card, neither not-enrolled nor tenant-unresolved.
describe('App resolution-error route', () => {
  it('shows the resolution-error card, offering no enroll command, for a RESOLUTION_FAILED 401', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        const url = input instanceof URL ? input.href : input;
        if (url.includes('/v1/platform')) {
          return Promise.resolve(jsonResponse(PLATFORM));
        }
        if (url.includes('/v1/config')) {
          return Promise.resolve({
            ok: false,
            status: 401,
            json: () =>
              Promise.resolve({
                code: 'RESOLUTION_FAILED',
                message: 'identity could not be resolved because of an internal error',
              }),
          } as unknown as Response);
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );

    renderWithStore(<App />);

    await screen.findByText('Could not resolve your identity');
    expect(screen.queryByText('Not yet part of a tenant')).not.toBeInTheDocument();
    expect(screen.queryByText('Could not determine your tenant')).not.toBeInTheDocument();
    expect(screen.queryByText(/erun platform user enroll/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
  });
});

import { cleanup, screen, waitFor } from '@testing-library/react';
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
});

describe('App signed-out route', () => {
  // The regression this guards: the old signed-out screen was a CenteredCard
  // wrapping a CardTitle div and a paragraph — zero real <h1>-<h6> elements,
  // per erun#1327 ("no <h1> and no <main> landmark"). This same assertion,
  // run against origin/main's SignInScreen, fails with "expected length to be
  // greater than 1, received 0" (CardTitle is a <div>, not a heading) — see
  // the PR description for the recorded red run. It passes here because
  // LandingScreen renders a real <h1> pitch and an <h2> differentiators
  // heading inside a <main> landmark.
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

  // The bundled defaults are what an unconfigured instance shows, and they are
  // what every instance showed while the platform still had no way to send
  // these three fields — so the configured case needs its own coverage, end to
  // end from the discovery response to the rendered page.
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

    // The bundled tagline, the public docs site, and the generic mark — a
    // half-configured instance renders a coherent page, never a blank hero or
    // a broken image.
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

// This is the acceptance-criteria-level check: the console never keeps the
// old token and relabels which tenant it claims to be on. A switch attempt is
// only real once the API itself resolves the new token to the requested
// tenant — a caller that comes back resolved to some *other* tenant (still
// signed into the same account, chose a different one, whatever the cause)
// must be told, not silently shown as if the switch worked.
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

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { type TenantConfigView, TooltipProvider } from 'erun-kit';
import { Provider } from 'react-redux';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createAppStore } from '../app/store';
import { AppShell } from './AppShell';

// Mocked so scope-selector tests can assert scope changes never reach the OIDC redirect.
vi.mock('../auth/auth', () => ({
  beginLogin: vi.fn(),
}));

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

// Mount-time fetches need only a blanket 200-empty response.
function stubFetch(whoamiBody: Record<string, unknown> = {}): void {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL) => {
      const url = input instanceof URL ? input.href : input;
      if (url.includes('/v1/whoami')) {
        return Promise.resolve(jsonResponse(whoamiBody));
      }
      if (url.includes('/v1/identity/users') || url.includes('/v1/tenants')) {
        return Promise.resolve(jsonResponse([]));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
}

function fakeToken(payload: Record<string, unknown>): string {
  const segment = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `${segment({ alg: 'none' })}.${segment(payload)}.sig`;
}

const OPERATIONS_CONFIG: TenantConfigView = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'OPERATIONS' },
  environments: [],
  contexts: [],
  inviteRequestRateLimitWindowSeconds: 60,
};

const COMPANY_CONFIG: TenantConfigView = {
  tenant: { tenantId: 'tn-2', name: 'Beta', type: 'COMPANY' },
  environments: [],
  contexts: [],
  inviteRequestRateLimitWindowSeconds: 60,
};

function renderShell(config: TenantConfigView, token = 'dev-token'): void {
  render(
    <Provider store={createAppStore()}>
      <TooltipProvider>
        <AppShell
          brand="Acme"
          token={token}
          config={config}
          oidc={undefined}
          switchMismatch={undefined}
          onDismissSwitchMismatch={vi.fn()}
          onChanged={vi.fn()}
          onSignOut={vi.fn()}
        />
      </TooltipProvider>
    </Provider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

describe('AppShell navigation', () => {
  it('shows the operations-only sections, including Tenants, for an OPERATIONS tenant', () => {
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    expect(nav.getByRole('button', { name: /Tenants/ })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Users/ })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Org settings/ })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Outbound mail/ })).toBeInTheDocument();
  });

  it('hides the operations-only sections, including Tenants, for a non-OPERATIONS tenant', () => {
    stubFetch();
    renderShell(COMPANY_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    expect(nav.queryByRole('button', { name: /Tenants/ })).not.toBeInTheDocument();
    expect(nav.queryByRole('button', { name: /Users/ })).not.toBeInTheDocument();
    expect(nav.queryByRole('button', { name: /Org settings/ })).not.toBeInTheDocument();
    expect(nav.queryByRole('button', { name: /Outbound mail/ })).not.toBeInTheDocument();
  });

  it('switches the main pane and keeps exactly one nav item current', () => {
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));

    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Overview/ })).toHaveAttribute('aria-current', 'page');

    fireEvent.click(nav.getByRole('button', { name: /Environments/ }));

    expect(screen.queryByRole('heading', { level: 2, name: 'Acme' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1, name: 'Environments' })).toBeInTheDocument();
    expect(screen.getByText('Register and deploy environments')).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Environments/ })).toHaveAttribute(
      'aria-current',
      'page',
    );
    expect(nav.getByRole('button', { name: /Overview/ })).not.toHaveAttribute('aria-current');

    const current = nav.getAllByRole('button').filter((el) => el.getAttribute('aria-current'));
    expect(current).toHaveLength(1);
  });
});

describe('AppShell section URL sync', () => {
  it('pushes a history entry for the URL when navigating between sections', () => {
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));

    fireEvent.click(nav.getByRole('button', { name: /Environments/ }));
    expect(window.location.pathname).toBe('/environments');

    fireEvent.click(nav.getByRole('button', { name: /Overview/ }));
    expect(window.location.pathname).toBe('/');
  });

  it('renders the section named by the URL on mount', () => {
    window.history.pushState(null, '', '/environments');
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    expect(screen.getByRole('heading', { level: 1, name: 'Environments' })).toBeInTheDocument();
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    expect(nav.getByRole('button', { name: /Environments/ })).toHaveAttribute(
      'aria-current',
      'page',
    );
  });

  it('falls back to Overview, and corrects the URL, for an OPERATIONS-only section on a COMPANY tenant', () => {
    window.history.pushState(null, '', '/users');
    stubFetch();
    renderShell(COMPANY_CONFIG);
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/');
  });

  it('moves between sections when a popstate fires (Back/Forward)', () => {
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));

    fireEvent.click(nav.getByRole('button', { name: /Environments/ }));
    expect(screen.getByRole('heading', { level: 1, name: 'Environments' })).toBeInTheDocument();

    // jsdom's history.back() is async, so the URL is moved directly and popstate dispatched by hand.
    act(() => {
      window.history.pushState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeInTheDocument();

    act(() => {
      window.history.pushState(null, '', '/environments');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.getByRole('heading', { level: 1, name: 'Environments' })).toBeInTheDocument();
  });
});

describe('AppShell nav badge', () => {
  it('shows the pending-request count on the Requests nav item', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        const url = input instanceof URL ? input.href : input;
        if (url.includes('/v1/invite-requests')) {
          return Promise.resolve(
            jsonResponse([
              {
                inviteRequestId: 'ir-1',
                issuer: 'https://idp.example.com',
                subject: 'sub-1',
                kind: 'JOIN_TENANT',
                tenantName: 'beta',
                status: 'PENDING',
                createdAt: '2026-06-24T10:00:00Z',
                updatedAt: '2026-06-24T10:00:00Z',
              },
            ]),
          );
        }
        return Promise.resolve(jsonResponse({}));
      }),
    );
    renderShell(COMPANY_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    // The badge appears only once the list query resolves.
    await waitFor(() => {
      const requestsButton = nav.getByRole('button', { name: /Requests/ });
      expect(requestsButton).toHaveTextContent('1');
    });
  });

  it('renders no badge when there are no pending requests', async () => {
    stubFetch();
    renderShell(COMPANY_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    const requestsButton = await nav.findByRole('button', { name: 'Requests' });
    expect(requestsButton).toHaveTextContent('Requests');
    expect(requestsButton.querySelector('[aria-label$="pending"]')).toBeNull();
  });
});

describe('AppShell identity chrome', () => {
  it('signs out through the header action', () => {
    stubFetch();
    const onSignOut = vi.fn();
    render(
      <Provider store={createAppStore()}>
        <TooltipProvider>
          <AppShell
            brand="Acme"
            token="dev-token"
            config={OPERATIONS_CONFIG}
            oidc={undefined}
            switchMismatch={undefined}
            onDismissSwitchMismatch={vi.fn()}
            onChanged={vi.fn()}
            onSignOut={onSignOut}
          />
        </TooltipProvider>
      </Provider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });

  it('toggles the dark theme class on the document root', () => {
    stubFetch();
    document.documentElement.classList.remove('dark');
    renderShell(OPERATIONS_CONFIG);

    const toggle = screen.getByRole('button', { name: 'Switch to dark theme' });
    fireEvent.click(toggle);
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(screen.getByRole('button', { name: 'Switch to light theme' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Switch to light theme' }));
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it("prefers whoami's username over the token's email once whoami resolves", async () => {
    stubFetch({ username: 'erun' });
    renderShell(OPERATIONS_CONFIG, fakeToken({ sub: '123', email: 'someone@example.com' }));
    await waitFor(() => {
      expect(screen.getByText('erun')).toBeInTheDocument();
    });
    expect(screen.queryByText('someone@example.com')).not.toBeInTheDocument();
  });

  it("falls back to the token's email while whoami has not resolved yet", () => {
    stubFetch({ username: 'erun' });
    renderShell(OPERATIONS_CONFIG, fakeToken({ sub: '123', email: 'someone@example.com' }));
    // Asserted before fetch settles: the first paint has only the token claim.
    expect(screen.getByText('someone@example.com')).toBeInTheDocument();
  });

  it('never renders the raw subject id: a pending placeholder until whoami resolves, then the username', async () => {
    stubFetch({ username: 'erun' });
    const subject = '386994597031248060';
    renderShell(OPERATIONS_CONFIG, fakeToken({ sub: subject }));

    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(screen.queryByText(subject)).not.toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('erun')).toBeInTheDocument();
    });
    expect(screen.queryByText(subject)).not.toBeInTheDocument();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
  });
});

describe('AppShell scope selector', () => {
  const SCOPE_CONFIG: TenantConfigView = {
    tenant: { tenantId: 'tn-1', name: 'Acme', type: 'OPERATIONS' },
    environments: [
      { environmentId: 'env-1', name: 'acme-env', type: 'runtime', status: 'running' },
    ],
    contexts: [],
    inviteRequestRateLimitWindowSeconds: 60,
  };

  function stubScopeFetch(): void {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL) => {
        const url = input instanceof URL ? input.href : input;
        if (url === '/v1/tenants') {
          return Promise.resolve(
            jsonResponse([
              { tenantId: 'tn-1', name: 'Acme', type: 'OPERATIONS', createdAt: '', updatedAt: '' },
              { tenantId: 'tn-2', name: 'Beta', type: 'COMPANY', createdAt: '', updatedAt: '' },
            ]),
          );
        }
        if (url === '/v1/environments?tenantId=tn-2') {
          return Promise.resolve(
            jsonResponse([
              {
                environmentId: 'env-2',
                name: 'beta-env',
                type: 'runtime',
                status: 'running',
                tenantId: 'tn-2',
              },
            ]),
          );
        }
        return Promise.resolve(jsonResponse([]));
      }),
    );
  }

  it("swaps the Environments panel's rows on a scope change, and never re-authenticates", async () => {
    stubScopeFetch();
    const { beginLogin } = await import('../auth/auth');
    renderShell(SCOPE_CONFIG);

    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    fireEvent.click(nav.getByRole('button', { name: /Environments/ }));
    // The AI-sessions panel also renders the name.
    expect(screen.getAllByText('acme-env').length).toBeGreaterThan(0);

    const scopeSelect = await screen.findByRole('combobox', { name: 'Administering' });
    fireEvent.click(scopeSelect);
    fireEvent.click(await screen.findByRole('option', { name: 'Beta' }));

    const betaRows = await screen.findAllByText('beta-env');
    // Only the deploy list's row carries the tenant badge.
    const betaRow = betaRows
      .map((el) => el.closest('li'))
      .find((li) => li !== null && within(li).queryByText('Beta') !== null);
    expect(betaRow).not.toBeUndefined();
    expect(within(betaRow as HTMLElement).getByText('Beta')).toBeInTheDocument();
    expect(screen.queryByText('acme-env')).not.toBeInTheDocument();
    expect(beginLogin).not.toHaveBeenCalled();
  });
});

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { type TenantConfigView, TooltipProvider } from 'erun-kit';
import { Provider } from 'react-redux';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createAppStore } from '../app/store';
import { AppShell } from './AppShell';

// Mocked so the scope-selector tests below can assert the negative: unlike
// the tenant switcher, changing scope must never reach the OIDC redirect
// path. Type-only elsewhere in this module, so mocking the runtime export
// changes nothing for the tests that don't touch it.
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

// The identity panels and Tenants fetch on mount (Users, Org settings,
// Outbound mail, Tenants); every other section fetches only on submit. A
// blanket 200-empty response is enough to let navigating into any of them
// without erroring. whoamiBody defaults to an empty object (no username) so
// tests that don't care about the header's identity label are unaffected.
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

// fakeToken builds a decodable (unsigned) JWT-shaped string carrying the
// given claims -- readTokenIdentity only decodes the payload, so a real
// signature is unnecessary here.
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

  // The negative case matters as much as the positive one here: Tenants
  // registration is the one action only an OPERATIONS tenant may take, so a
  // COMPANY-tenant Operator must not even see a control that would refuse
  // them, the same rule Users/Org settings/Outbound mail already follow.
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

    // Overview is the default section: the tenant name renders, the header
    // title matches, and only the Overview nav item is marked current.
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Acme' })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Overview/ })).toHaveAttribute('aria-current', 'page');

    fireEvent.click(nav.getByRole('button', { name: /Environments/ }));

    // The main pane switched wholesale: the overview content is gone, the
    // environments panel is showing, and selection moved to exactly one item.
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

    // The URL moves without going through onSelect, so only a real popstate
    // event -- not a click -- exercises the listener. jsdom's own
    // history.back()/forward() resolve asynchronously, so the URL is moved
    // directly and the event dispatched by hand to keep this deterministic.
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
  // The pending count must be visible without opening the Requests panel --
  // this asserts the count renders on the nav item itself, never requiring
  // a click into the panel.
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
    // The badge only appears once the list query resolves — the button
    // itself renders immediately with just the "Requests" label, so this
    // must wait for the count rather than asserting on the first match.
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
    // Asserted synchronously, before the mocked fetch's promise settles: this
    // is the header's very first paint, with only the token claim available.
    expect(screen.getByText('someone@example.com')).toBeInTheDocument();
  });

  it('never renders the raw subject id: a pending placeholder until whoami resolves, then the username', async () => {
    stubFetch({ username: 'erun' });
    const subject = '386994597031248060';
    renderShell(OPERATIONS_CONFIG, fakeToken({ sub: subject }));

    // No email claim and whoami not yet resolved -- must show that the label
    // is unresolved, never the numeric subject id.
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(screen.queryByText(subject)).not.toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('erun')).toBeInTheDocument();
    });
    expect(screen.queryByText(subject)).not.toBeInTheDocument();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
  });
});

// erun#1816: the scope selector is a second, distinct tenant control next to
// the (label-only, since oidc is undefined here too) tenant switcher above
// the nav -- picking a target must swap which tenant's environments render
// without ever reaching the switcher's re-auth path.
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
    // Both the deploy list and the AI-sessions panel render the environment
    // name, so this asserts presence rather than a single match.
    expect(screen.getAllByText('acme-env').length).toBeGreaterThan(0);

    const scopeSelect = await screen.findByRole('combobox', { name: 'Administering' });
    fireEvent.click(scopeSelect);
    fireEvent.click(await screen.findByRole('option', { name: 'Beta' }));

    const betaRows = await screen.findAllByText('beta-env');
    // Only the deploy list's row carries the owning-tenant badge; the
    // AI-sessions panel's row does not, so this picks that one out.
    const betaRow = betaRows
      .map((el) => el.closest('li'))
      .find((li) => li !== null && within(li).queryByText('Beta') !== null);
    expect(betaRow).not.toBeUndefined();
    expect(within(betaRow as HTMLElement).getByText('Beta')).toBeInTheDocument();
    expect(screen.queryByText('acme-env')).not.toBeInTheDocument();
    expect(beginLogin).not.toHaveBeenCalled();
  });
});

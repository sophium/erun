import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { type TenantConfigView, TooltipProvider } from 'erun-kit';
import { Provider } from 'react-redux';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createAppStore } from '../app/store';
import { AppShell } from './AppShell';

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
// without erroring.
function stubFetch(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL) => {
      const url = input instanceof URL ? input.href : input;
      if (url.includes('/v1/identity/users') || url.includes('/v1/tenants')) {
        return Promise.resolve(jsonResponse([]));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
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

function renderShell(config: TenantConfigView): void {
  render(
    <Provider store={createAppStore()}>
      <TooltipProvider>
        <AppShell
          brand="Acme"
          token="dev-token"
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
});

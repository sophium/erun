import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { TooltipProvider } from 'erun-kit';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { TenantConfigView } from '../config/types';
import { AppShell } from './AppShell';

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

// Only the identity panels fetch on mount (Users, Org settings); every other
// section fetches only on submit. A blanket 200-empty response is enough to
// let navigating into either without erroring.
function stubFetch(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string | URL) => {
      const url = input instanceof URL ? input.href : input;
      if (url.includes('/v1/identity/users')) {
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
};

const COMPANY_CONFIG: TenantConfigView = {
  tenant: { tenantId: 'tn-2', name: 'Beta', type: 'COMPANY' },
  environments: [],
  contexts: [],
};

function renderShell(config: TenantConfigView): void {
  render(
    <TooltipProvider>
      <AppShell
        brand="Acme"
        token="dev-token"
        config={config}
        onChanged={vi.fn()}
        onSignOut={vi.fn()}
      />
    </TooltipProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('AppShell navigation', () => {
  it('shows the identity sections only for an OPERATIONS tenant', () => {
    stubFetch();
    renderShell(OPERATIONS_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    expect(nav.getByRole('button', { name: /Users/ })).toBeInTheDocument();
    expect(nav.getByRole('button', { name: /Org settings/ })).toBeInTheDocument();
  });

  it('hides the identity sections for a non-OPERATIONS tenant', () => {
    stubFetch();
    renderShell(COMPANY_CONFIG);
    const nav = within(screen.getByRole('navigation', { name: 'Console sections' }));
    expect(nav.queryByRole('button', { name: /Users/ })).not.toBeInTheDocument();
    expect(nav.queryByRole('button', { name: /Org settings/ })).not.toBeInTheDocument();
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

describe('AppShell identity chrome', () => {
  it('signs out through the header action', () => {
    stubFetch();
    const onSignOut = vi.fn();
    render(
      <TooltipProvider>
        <AppShell
          brand="Acme"
          token="dev-token"
          config={OPERATIONS_CONFIG}
          onChanged={vi.fn()}
          onSignOut={onSignOut}
        />
      </TooltipProvider>,
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

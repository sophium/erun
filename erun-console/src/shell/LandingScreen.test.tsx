import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';
import { LandingScreen } from './LandingScreen';

vi.mock('../auth/auth', () => ({
  beginLogin: vi.fn(),
}));

const OIDC: OidcConfig = {
  issuer: 'https://auth.acme.example',
  clientId: 'console-client',
  redirectUri: 'http://localhost/',
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('LandingScreen content', () => {
  it('renders a real landmark structure with the pitch, the differentiators, and both CTAs', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline="Acme's own tagline."
        docsUrl="https://docs.acme.example"
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    expect(screen.getByRole('banner')).toBeInTheDocument();
    expect(screen.getByRole('main')).toBeInTheDocument();
    expect(screen.getByRole('contentinfo')).toBeInTheDocument();

    const h1 = screen.getByRole('heading', { level: 1, name: "Acme's own tagline." });
    const h2 = screen.getByRole('heading', { level: 2, name: 'What makes Acme different' });
    expect(h1).toBeInTheDocument();
    expect(h2).toBeInTheDocument();
    // Heading order: the pitch (h1) must read before the differentiators (h2).
    expect(h1.compareDocumentPosition(h2) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    expect(screen.getAllByRole('link', { name: 'Read the docs' })).toHaveLength(2);
    expect(screen.getAllByRole('link', { name: 'Read the docs' })[0]).toHaveAttribute(
      'href',
      'https://docs.acme.example',
    );
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
  });

  it("renders the instance's own logo in the header when one is configured", () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl="https://cdn.acme.example/logo.svg"
        tagline={undefined}
        docsUrl={undefined}
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    const header = screen.getByRole('banner');
    expect(header.querySelector('img')).toHaveAttribute('src', 'https://cdn.acme.example/logo.svg');
  });

  it('falls back to the bundled tagline and brand when the instance sets none', () => {
    render(
      <LandingScreen
        brand={undefined}
        logoUrl={undefined}
        tagline={undefined}
        docsUrl={undefined}
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    expect(
      screen.getByRole('heading', {
        level: 1,
        name: 'Agentic coding from idea to production, without compromising compliance.',
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { level: 2, name: 'What makes ERun different' }),
    ).toBeInTheDocument();
  });

  it('falls back to the public docs site in the footer when docsUrl is unset, in the signed-out state', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline={undefined}
        docsUrl={undefined}
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    // The hero's primary "Read the docs" CTA still renders only when an
    // instance configures a real docsUrl (never a hardcoded fallback host),
    // but the footer is the one link every visitor can always reach, so it
    // falls back rather than disappearing.
    const docsLinks = screen.getAllByRole('link', { name: 'Read the docs' });
    expect(docsLinks).toHaveLength(1);
    expect(docsLinks[0]).toHaveAttribute('href', 'https://docs.erunpaas.com');
  });

  it('gives the no-OIDC state a docs link too, in both the footer and the configure-OIDC copy', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline={undefined}
        docsUrl={undefined}
        oidc={undefined}
        fallbackReason={undefined}
      />,
    );

    // No Sign in button can work with no OIDC configured, so the state must
    // still offer a way forward: the footer's docs link, and the
    // explanation's own link to the page that walks through configuring one.
    expect(screen.getByRole('link', { name: 'Read the docs' })).toHaveAttribute(
      'href',
      'https://docs.erunpaas.com',
    );
    expect(
      screen.getByRole('link', { name: 'configure OIDC sign-in for this instance' }),
    ).toHaveAttribute('href', 'https://docs.erunpaas.com/deployment/deploy-platform#hosted-idp');
  });

  it('shows the bearer-token fallback note, not a Sign in button, when OIDC is unconfigured', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline={undefined}
        docsUrl={undefined}
        oidc={undefined}
        fallbackReason="local VITE_OIDC_* override"
      />,
    );

    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
    expect(
      screen.getByText(/A bearer token is required to view your environments\./),
    ).toBeInTheDocument();
    expect(screen.getByText('local VITE_OIDC_* override')).toBeInTheDocument();
  });

  it('starts the real OIDC redirect when Sign in is clicked', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline={undefined}
        docsUrl={undefined}
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(beginLogin).toHaveBeenCalledWith(OIDC);
  });

  it('stacks the CTAs and the differentiator cards single-column at a phone width', () => {
    render(
      <LandingScreen
        brand="Acme"
        logoUrl={undefined}
        tagline={undefined}
        docsUrl="https://docs.acme.example"
        oidc={OIDC}
        fallbackReason={undefined}
      />,
    );

    // jsdom does not run layout, so this asserts the responsive contract at
    // the class level: mobile-first with no override below `sm` (640px),
    // narrower than a 375px phone viewport — not a rendered pixel measurement.
    const ctaRow = screen.getByRole('button', { name: 'Sign in' }).parentElement;
    expect(ctaRow?.className).toContain('flex-col');
    expect(ctaRow?.className).toContain('sm:flex-row');

    const cardsGrid = screen
      .getByRole('heading', { level: 2 })
      .parentElement?.querySelector('.grid');
    expect(cardsGrid?.className).toContain('grid-cols-1');
    expect(cardsGrid?.className).not.toMatch(/(?<!sm:|lg:)grid-cols-[234]/);
  });
});

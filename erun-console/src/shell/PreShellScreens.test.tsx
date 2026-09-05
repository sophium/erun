import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  NotEnrolledScreen,
  ResolutionErrorScreen,
  TenantUnresolvedScreen,
} from './PreShellScreens';

// Builds a JWT-shaped string with the given payload — the same shape
// src/auth/identity.test.ts uses, since NotEnrolledScreen decodes the token
// the same way.
function tokenWith(payload: Record<string, unknown>): string {
  const encode = (value: string): string => {
    const bytes = new TextEncoder().encode(value);
    const binary = Array.from(bytes, (byte) => String.fromCharCode(byte)).join('');
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  };
  return `${encode('{"alg":"RS256"}')}.${encode(JSON.stringify(payload))}.signature`;
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

const oidc = {
  issuer: 'https://auth.example.com',
  clientId: 'console-client',
  redirectUri: 'http://localhost/',
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('NotEnrolledScreen', () => {
  it('names the user from the email claim and still shows the subject for the operator', () => {
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({
          sub: '387534471668170904',
          email: 'someone@example.com',
          iss: 'https://auth.example.com',
        })}
        oidc={undefined}
        onSignOut={vi.fn()}
      />,
    );

    const identityLine = screen.getByText(/Give them this identity:/);
    expect(identityLine).toHaveTextContent('someone@example.com');
    expect(identityLine).toHaveTextContent('subject 387534471668170904');
    expect(identityLine).toHaveTextContent('https://auth.example.com');
  });

  it('degrades to the subject alone, not a blank name, when the token carries no email claim', () => {
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904', iss: 'https://auth.example.com' })}
        oidc={undefined}
        onSignOut={vi.fn()}
      />,
    );

    const identityLine = screen.getByText(/Give them this identity:/);
    expect(identityLine).toHaveTextContent('387534471668170904');
    expect(identityLine).not.toHaveTextContent('subject 387534471668170904');
  });

  it('offers a sign-out so a wrongly-signed-in user can sign in as someone else', () => {
    const onSignOut = vi.fn();
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        oidc={undefined}
        onSignOut={onSignOut}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });

  it('does not claim sign-out can reach a different account with no OIDC config (dev-token fallback)', () => {
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        oidc={undefined}
        onSignOut={vi.fn()}
      />,
    );

    expect(
      screen.getByText(/cannot end your identity provider's session from here/),
    ).toBeInTheDocument();
  });

  it('does not claim sign-out can reach a different account when the IdP has no end_session_endpoint', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({
            authorization_endpoint: 'https://auth.example.com/authorize',
            token_endpoint: 'https://auth.example.com/token',
          }),
        ),
      ),
    );
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        oidc={oidc}
        onSignOut={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(
        screen.getByText(/cannot end your identity provider's session from here/),
      ).toBeInTheDocument();
    });
  });

  it('advises that sign-out also ends the IdP session once discovery confirms end_session_endpoint', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({
            authorization_endpoint: 'https://auth.example.com/authorize',
            token_endpoint: 'https://auth.example.com/token',
            end_session_endpoint: 'https://auth.example.com/endsession',
          }),
        ),
      ),
    );
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        oidc={oidc}
        onSignOut={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText(/it also/)).toHaveTextContent(
        'it also ends your identity provider session',
      );
    });
    expect(
      screen.queryByText(/cannot end your identity provider's session from here/),
    ).not.toBeInTheDocument();
  });

  it('offers the filled-in enroll command and copies it', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(
      <NotEnrolledScreen
        brand="Acme"
        token={tokenWith({
          sub: '387534471668170904',
          email: 'someone@example.com',
          iss: 'https://auth.example.com',
        })}
        oidc={undefined}
        onSignOut={vi.fn()}
      />,
    );

    const expectedCommand =
      'erun platform user enroll --username someone@example.com --issuer https://auth.example.com --subject 387534471668170904';
    expect(screen.getByText(expectedCommand)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith(expectedCommand);
  });
});

// erun#1721: this screen is the "unresolvable" counterpart to NotEnrolledScreen
// above -- shown when the API cannot tell which tenant a token resolves to,
// as distinct from a genuinely-unenrolled identity. It must not repeat the
// "an operator must enrol you" copy, since the caller may already be enrolled.
describe('TenantUnresolvedScreen', () => {
  it('shows the API-reported reason instead of the not-enrolled copy', () => {
    render(
      <TenantUnresolvedScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904', iss: 'https://auth.example.com' })}
        message='issuer "https://auth.example.com" is org-scoped but the token carries no matching claim'
        onSignOut={vi.fn()}
      />,
    );

    expect(screen.getByText(/could not tell which tenant/i)).toBeInTheDocument();
    expect(
      screen.getByText(/is org-scoped but the token carries no matching claim/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/an operator has to enrol you/i)).not.toBeInTheDocument();
  });

  it('names the identity from the token the same way NotEnrolledScreen does', () => {
    render(
      <TenantUnresolvedScreen
        brand="Acme"
        token={tokenWith({
          sub: '387534471668170904',
          email: 'rihards@frs.lv',
          iss: 'https://auth.erunpaas.com',
        })}
        message="unresolved"
        onSignOut={vi.fn()}
      />,
    );

    const identityLine = screen.getByText(/Identity:/);
    expect(identityLine).toHaveTextContent('rihards@frs.lv');
    expect(identityLine).toHaveTextContent('https://auth.erunpaas.com');
  });

  it('offers a sign-out', () => {
    const onSignOut = vi.fn();
    render(
      <TenantUnresolvedScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        message="unresolved"
        onSignOut={onSignOut}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });
});

// erun#1752: shown when resolution failed for an internal reason (already
// sanitized server-side into a safe message) rather than a real answer about
// enrolment. It must never claim the caller isn't enrolled, and -- unlike
// NotEnrolledScreen -- must never offer an enroll command: whatever link
// this identity has, it was not what failed.
describe('ResolutionErrorScreen', () => {
  it('shows the API-reported reason instead of the not-enrolled copy, with no enroll command', () => {
    render(
      <ResolutionErrorScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904', iss: 'https://auth.erunpaas.com' })}
        message="identity could not be resolved because of an internal error"
        onSignOut={vi.fn()}
      />,
    );

    expect(screen.getByText(/could not resolve your identity/i)).toBeInTheDocument();
    expect(
      screen.getByText(/identity could not be resolved because of an internal error/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/an operator has to enrol you/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/erun platform user enroll/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
  });

  it('names the identity from the token the same way the other pre-shell screens do', () => {
    render(
      <ResolutionErrorScreen
        brand="Acme"
        token={tokenWith({
          sub: '387534471668170904',
          email: 'rihards@frs.lv',
          iss: 'https://auth.erunpaas.com',
        })}
        message="internal error"
        onSignOut={vi.fn()}
      />,
    );

    const identityLine = screen.getByText(/Identity:/);
    expect(identityLine).toHaveTextContent('rihards@frs.lv');
    expect(identityLine).toHaveTextContent('https://auth.erunpaas.com');
  });

  it('offers a sign-out', () => {
    const onSignOut = vi.fn();
    render(
      <ResolutionErrorScreen
        brand="Acme"
        token={tokenWith({ sub: '387534471668170904' })}
        message="internal error"
        onSignOut={onSignOut}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });
});

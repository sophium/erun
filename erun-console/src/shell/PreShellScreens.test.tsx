import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { NotEnrolledScreen, TenantUnresolvedScreen } from './PreShellScreens';

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

afterEach(() => {
  cleanup();
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
        onSignOut={onSignOut}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(onSignOut).toHaveBeenCalledTimes(1);
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

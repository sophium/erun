import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { NotEnrolledScreen } from './PreShellScreens';

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

import { describe, expect, it } from 'vitest';

import { readTokenIdentity } from './identity';

// Builds a JWT-shaped string with the given payload. Only the payload segment
// is ever read, so the header and signature are placeholders.
function tokenWith(payload: Record<string, unknown>): string {
  const encode = (value: string): string => {
    // UTF-8 first, then base64url — what a real issuer emits. Passing the
    // string straight to btoa would encode Latin-1 bytes and produce a token no
    // issuer would ever mint.
    const bytes = new TextEncoder().encode(value);
    const binary = Array.from(bytes, (byte) => String.fromCharCode(byte)).join('');
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  };
  return `${encode('{"alg":"RS256"}')}.${encode(JSON.stringify(payload))}.signature`;
}

describe('readTokenIdentity', () => {
  it('reads the claims an operator needs in order to enrol someone', () => {
    const identity = readTokenIdentity(
      tokenWith({
        sub: '387534471668170904',
        email: 'someone@example.com',
        iss: 'https://auth.example.com',
      }),
    );

    expect(identity.subject).toBe('387534471668170904');
    expect(identity.email).toBe('someone@example.com');
    expect(identity.issuer).toBe('https://auth.example.com');
  });

  it('falls back to preferred_username when there is no email claim', () => {
    expect(readTokenIdentity(tokenWith({ preferred_username: 'someone@example.com' })).email).toBe(
      'someone@example.com',
    );
  });

  it('decodes a payload containing non-ASCII rather than mangling it', () => {
    expect(readTokenIdentity(tokenWith({ email: 'zoë@example.com' })).email).toBe(
      'zoë@example.com',
    );
  });

  it('returns nothing for a token that is not a readable JWT, rather than throwing', () => {
    // The dev-bearer fallback is an opaque string, and this runs on the screen a
    // stuck user sees -- throwing there would replace an explanation with a
    // blank page.
    for (const value of ['', 'dev-token', 'a.b', 'a.b.c.d', 'not-base64.$$$.sig']) {
      const identity = readTokenIdentity(value);
      expect(identity.subject).toBeUndefined();
      expect(identity.email).toBeUndefined();
    }
  });

  it('ignores a payload that decodes to something other than an object', () => {
    expect(
      readTokenIdentity(tokenWith([] as unknown as Record<string, unknown>)).subject,
    ).toBeUndefined();
    expect(
      readTokenIdentity(`x.${btoa('"a string"').replace(/=+$/, '')}.y`).subject,
    ).toBeUndefined();
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  completeLogin,
  devBearerToken,
  isAuthCallback,
  oidcConfig,
  resolveOidcConfig,
  resolveToken,
  storedToken,
} from './auth';

// Unit coverage for the OIDC Authorization Code + PKCE mechanics with the
// discovery + token endpoints mocked. The full browser sign-in against a real
// Zitadel v4 is exercised separately by the Playwright suite in ../../playwright;
// this locks the callback exchange, state validation, config gating, and the
// dev-token fallback.

const DISCOVERY = {
  authorization_endpoint: 'http://localhost:8080/oauth/v2/authorize',
  token_endpoint: 'http://localhost:8080/oauth/v2/token',
};

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

function setCallbackUrl(search: string): void {
  window.history.replaceState({}, '', '/' + search);
}

beforeEach(() => {
  sessionStorage.clear();
  setCallbackUrl('');
});

afterEach(() => {
  cleanup();
});

function cleanup(): void {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  sessionStorage.clear();
  setCallbackUrl('');
}

const config = {
  issuer: 'http://localhost:8080',
  clientId: 'console-client',
  redirectUri: 'http://localhost/',
};

describe('oidcConfig', () => {
  it('is undefined when issuer/clientId are unset (dev-token fallback applies)', () => {
    vi.stubEnv('VITE_OIDC_ISSUER', '');
    vi.stubEnv('VITE_OIDC_CLIENT_ID', '');
    expect(oidcConfig()).toBeUndefined();
  });

  it('reads the issuer + client id and derives the origin redirect URI', () => {
    vi.stubEnv('VITE_OIDC_ISSUER', 'http://localhost:8080');
    vi.stubEnv('VITE_OIDC_CLIENT_ID', 'console-client');
    const cfg = oidcConfig();
    expect(cfg).toEqual({
      issuer: 'http://localhost:8080',
      clientId: 'console-client',
      redirectUri: window.location.origin + '/',
    });
  });
});

describe('isAuthCallback', () => {
  it('detects ?code=&state=', () => {
    expect(isAuthCallback()).toBe(false);
    setCallbackUrl('?code=abc&state=xyz');
    expect(isAuthCallback()).toBe(true);
  });
});

describe('completeLogin', () => {
  it('exchanges the code + verifier for an id_token and stores it', async () => {
    sessionStorage.setItem('erun.console.pkceVerifier', 'the-verifier');
    sessionStorage.setItem('erun.console.oauthState', 'state-123');
    setCallbackUrl('?code=auth-code&state=state-123');

    const calls: { url: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL, init?: RequestInit) => {
        const url = input instanceof URL ? input.href : input;
        calls.push({ url, body: init?.body });
        if (url.includes('.well-known/openid-configuration')) {
          return Promise.resolve(jsonResponse(DISCOVERY));
        }
        return Promise.resolve(jsonResponse({ id_token: 'the.jwt.token', access_token: 'opaque' }));
      }),
    );

    const token = await completeLogin(config);
    expect(token).toBe('the.jwt.token');
    expect(storedToken()).toBe('the.jwt.token');
    // The token exchange posts the code + the PKCE verifier to the token endpoint.
    const exchange = calls.find((c) => c.url === DISCOVERY.token_endpoint);
    expect(exchange).toBeDefined();
    const params = new URLSearchParams(exchange?.body as string);
    expect(params.get('grant_type')).toBe('authorization_code');
    expect(params.get('code')).toBe('auth-code');
    expect(params.get('code_verifier')).toBe('the-verifier');
    expect(params.get('client_id')).toBe('console-client');
  });

  it('rejects a state mismatch (CSRF guard) and stores no token', async () => {
    sessionStorage.setItem('erun.console.pkceVerifier', 'the-verifier');
    sessionStorage.setItem('erun.console.oauthState', 'expected-state');
    setCallbackUrl('?code=auth-code&state=attacker-state');
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );

    await expect(completeLogin(config)).rejects.toThrow(/state mismatch|invalid/i);
    expect(storedToken()).toBeUndefined();
  });
});

describe('resolveToken', () => {
  it('falls back to the dev token when OIDC is not configured and no callback', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'dev-token');
    expect(await resolveToken(undefined)).toBe('dev-token');
  });

  it('returns undefined when there is no token at all', async () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', '');
    expect(await resolveToken(undefined)).toBeUndefined();
  });
});

describe('devBearerToken', () => {
  it('reads VITE_DEV_BEARER_TOKEN', () => {
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', 'tok');
    expect(devBearerToken()).toBe('tok');
  });
});

describe('resolveOidcConfig', () => {
  it('prefers platform discovery (GET /v1/platform) over the VITE_* override', async () => {
    vi.stubEnv('VITE_OIDC_ISSUER', 'http://should-not-be-used');
    vi.stubEnv('VITE_OIDC_CLIENT_ID', 'should-not-be-used');
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          jsonResponse({
            issuer: 'https://auth.platform.example',
            consoleClientId: 'platform-client',
          }),
        ),
      ),
    );

    const resolution = await resolveOidcConfig();
    expect(resolution).toEqual({
      config: {
        issuer: 'https://auth.platform.example',
        clientId: 'platform-client',
        redirectUri: window.location.origin + '/',
      },
    });
  });

  it('falls back to the VITE_* override with a reason when discovery is absent (404)', async () => {
    vi.stubEnv('VITE_OIDC_ISSUER', 'http://localhost:8080');
    vi.stubEnv('VITE_OIDC_CLIENT_ID', 'console-client');
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse('not found', 404))),
    );

    const resolution = await resolveOidcConfig();
    expect(resolution.config).toEqual({
      issuer: 'http://localhost:8080',
      clientId: 'console-client',
      redirectUri: window.location.origin + '/',
    });
    expect(resolution.fallbackReason).toMatch(/platform discovery/i);
  });

  it('resolves to no config (dev-token fallback applies) when neither source is configured', async () => {
    vi.stubEnv('VITE_OIDC_ISSUER', '');
    vi.stubEnv('VITE_OIDC_CLIENT_ID', '');
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse('not found', 404))),
    );

    expect(await resolveOidcConfig()).toEqual({ config: undefined });
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  beginLogin,
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
  vi.restoreAllMocks();
  restoreLocation();
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

// jsdom's Location.assign is non-configurable, so it cannot be vi.spyOn'd or
// redefined in place; replacing window.location itself with a stub object is
// the standard workaround. realLocation lets restoreLocation put the live
// jsdom Location back afterwards, so later tests' history.replaceState-based
// navigation (setCallbackUrl) keeps working against the real thing.
let realLocation: Location | undefined;

function stubLocationAssign(): ReturnType<typeof vi.fn> {
  const assign = vi.fn();
  realLocation = window.location;
  // beginLogin only ever calls .assign(...) on window.location; origin,
  // pathname, and search are copied through (plain data properties, unlike
  // the real Location's own methods) because resolveToken's invalid_scope
  // retry reads search (via authCallbackError) before calling beginLogin, and
  // cleanCallbackUrl reads origin/pathname to rewrite the visible URL.
  Object.defineProperty(window, 'location', {
    value: {
      assign,
      origin: realLocation.origin,
      pathname: realLocation.pathname,
      search: realLocation.search,
    },
    writable: true,
    configurable: true,
  });
  return assign;
}

function restoreLocation(): void {
  if (realLocation === undefined) {
    return;
  }
  Object.defineProperty(window, 'location', {
    value: realLocation,
    writable: true,
    configurable: true,
  });
  realLocation = undefined;
}

describe('beginLogin', () => {
  it('requests no prompt for an ordinary sign-in', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );
    const assign = stubLocationAssign();

    await beginLogin(config);

    const url = new URL(assign.mock.calls[0]?.[0] as string);
    expect(url.searchParams.has('prompt')).toBe(false);
  });

  // The tenant switcher (shell/tenantSwitch.ts) passes select_account so the
  // IdP offers an account/org picker instead of silently reusing whatever
  // session it already holds — without this, "switch" could never land on a
  // different tenant for a caller still signed into the browser.
  it('carries an explicit prompt through to the authorization request', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );
    const assign = stubLocationAssign();

    await beginLogin(config, 'select_account');

    const url = new URL(assign.mock.calls[0]?.[0] as string);
    expect(url.searchParams.get('prompt')).toBe('select_account');
  });

  // erun#1721: without this scope a shared, org-scoped issuer's token carries
  // no org claim, so an already-enrolled operator's console session cannot
  // resolve its tenant even though the CLI/desktop (which already request
  // this scope by default) resolve fine.
  it('requests the org-claim scope by default', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );
    const assign = stubLocationAssign();

    await beginLogin(config);

    const url = new URL(assign.mock.calls[0]?.[0] as string);
    expect(url.searchParams.get('scope')).toBe(
      'openid profile email urn:zitadel:iam:user:resourceowner',
    );
  });

  it('omits the org-claim scope when told not to include it (the invalid_scope retry)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );
    const assign = stubLocationAssign();

    await beginLogin(config, undefined, false);

    const url = new URL(assign.mock.calls[0]?.[0] as string);
    expect(url.searchParams.get('scope')).toBe('openid profile email');
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

  // erun#1721: a dedicated/BYO issuer has never heard of the org-claim scope
  // and refuses the authorization request with error=invalid_scope. resolveToken
  // must retry sign-in once, without that scope, rather than treating the
  // refusal as "no token" (which would loop back to the landing screen).
  it('retries sign-in without the org-claim scope after an invalid_scope callback', async () => {
    setCallbackUrl('?error=invalid_scope&error_description=nope');
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse(DISCOVERY))),
    );
    const assign = stubLocationAssign();

    const result = await resolveToken(config);

    expect(result).toBeUndefined();
    expect(assign).toHaveBeenCalledTimes(1);
    const url = new URL(assign.mock.calls[0]?.[0] as string);
    expect(url.searchParams.get('scope')).toBe('openid profile email');
  });

  it('does not retry a second time in the same session', async () => {
    sessionStorage.setItem('erun.console.oidcOrgScopeRetried', '1');
    setCallbackUrl('?error=invalid_scope&error_description=nope');
    vi.stubEnv('VITE_DEV_BEARER_TOKEN', '');
    const assign = stubLocationAssign();

    const result = await resolveToken(config);

    expect(result).toBeUndefined();
    expect(assign).not.toHaveBeenCalled();
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

// OIDC Authorization Code + PKCE sign-in against the platform issuer (Zitadel),
// plus the dev-token fallback. The console redirects the Operator to the
// instance's OIDC issuer, completes the PKCE code exchange on the callback, and
// holds the resulting JWT to present as `Authorization: Bearer <jwt>` to
// erun-backend-api (whose OIDC verifier discovers the issuer's JWKS and checks
// the signature — see api-protocol.md § Sign-in). The id_token (always a JWT) is
// used as the bearer; Zitadel access tokens may be opaque.
//
// The issuer + client id are configuration (Vite env), never hardcoded — the
// console is installable as an independent PaaS instance. When OIDC is not
// configured, the dev token (VITE_DEV_BEARER_TOKEN) is used so local dev / e2e
// can drive the read view with a desktop-signed or dev token.

const TOKEN_STORAGE_KEY = 'erun.console.idToken';
const PKCE_VERIFIER_KEY = 'erun.console.pkceVerifier';
const PKCE_STATE_KEY = 'erun.console.oauthState';

export interface OidcConfig {
  issuer: string;
  clientId: string;
  redirectUri: string;
}

// oidcConfig returns the configured OIDC settings, or undefined when the console
// is not wired for OIDC (then the dev-token fallback applies). The redirect URI
// is the SPA's own origin — the callback is handled in-place (App detects the
// ?code= query), so no separate route is needed.
export function oidcConfig(): OidcConfig | undefined {
  const issuer = trimmed(import.meta.env.VITE_OIDC_ISSUER);
  const clientId = trimmed(import.meta.env.VITE_OIDC_CLIENT_ID);
  if (issuer === undefined || clientId === undefined) {
    return undefined;
  }
  return { issuer, clientId, redirectUri: window.location.origin + '/' };
}

function trimmed(value: string | undefined): string | undefined {
  if (typeof value !== 'string') {
    return undefined;
  }
  const v = value.trim();
  return v.length > 0 ? v : undefined;
}

interface Discovery {
  authorization_endpoint: string;
  token_endpoint: string;
}

async function discover(issuer: string): Promise<Discovery> {
  const response = await fetch(issuer.replace(/\/$/, '') + '/.well-known/openid-configuration');
  if (!response.ok) {
    throw new Error(`OIDC discovery failed (${String(response.status)})`);
  }
  const doc: unknown = await response.json();
  if (
    typeof doc !== 'object' ||
    doc === null ||
    typeof (doc as Discovery).authorization_endpoint !== 'string' ||
    typeof (doc as Discovery).token_endpoint !== 'string'
  ) {
    throw new Error('OIDC discovery document is missing endpoints');
  }
  return doc as Discovery;
}

function base64url(bytes: Uint8Array): string {
  let str = '';
  for (const b of bytes) {
    str += String.fromCharCode(b);
  }
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function randomString(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return base64url(bytes);
}

async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier));
  return base64url(new Uint8Array(digest));
}

// beginLogin starts the Authorization Code + PKCE flow: it mints a code verifier
// + state (stashed in sessionStorage for the callback), then redirects the
// browser to the issuer's authorization endpoint requesting an id_token via
// scope `openid profile email`.
export async function beginLogin(config: OidcConfig): Promise<void> {
  const discovery = await discover(config.issuer);
  const verifier = randomString();
  const state = randomString();
  sessionStorage.setItem(PKCE_VERIFIER_KEY, verifier);
  sessionStorage.setItem(PKCE_STATE_KEY, state);
  const params = new URLSearchParams({
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    response_type: 'code',
    scope: 'openid profile email',
    code_challenge: await pkceChallenge(verifier),
    code_challenge_method: 'S256',
    state,
  });
  window.location.assign(discovery.authorization_endpoint + '?' + params.toString());
}

// isAuthCallback reports whether the current URL is an OIDC redirect callback
// (carries ?code=&state=).
export function isAuthCallback(): boolean {
  const params = new URLSearchParams(window.location.search);
  return params.has('code') && params.has('state');
}

// completeLogin finishes the callback: it validates state, exchanges the code +
// PKCE verifier for tokens at the token endpoint, stores the id_token, clears
// the one-shot PKCE state, and strips the query from the URL. Returns the
// id_token to present to the API.
export async function completeLogin(config: OidcConfig): Promise<string> {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('code');
  const state = params.get('state');
  const verifier = sessionStorage.getItem(PKCE_VERIFIER_KEY);
  sessionStorage.removeItem(PKCE_VERIFIER_KEY);
  const expectedState = sessionStorage.getItem(PKCE_STATE_KEY);
  sessionStorage.removeItem(PKCE_STATE_KEY);
  cleanCallbackUrl();
  if (code === null || state === null || verifier === null || state !== expectedState) {
    throw new Error('OIDC callback is invalid (state mismatch or missing code)');
  }
  const discovery = await discover(config.issuer);
  const response = await fetch(discovery.token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: config.redirectUri,
      client_id: config.clientId,
      code_verifier: verifier,
    }).toString(),
  });
  if (!response.ok) {
    throw new Error(`OIDC token exchange failed (${String(response.status)})`);
  }
  const body: unknown = await response.json();
  const idToken =
    typeof body === 'object' && body !== null ? (body as { id_token?: unknown }).id_token : undefined;
  if (typeof idToken !== 'string' || idToken.length === 0) {
    throw new Error('OIDC token response had no id_token');
  }
  sessionStorage.setItem(TOKEN_STORAGE_KEY, idToken);
  return idToken;
}

function cleanCallbackUrl(): void {
  window.history.replaceState({}, document.title, window.location.origin + window.location.pathname);
}

// storedToken returns a previously-obtained id_token from this session.
export function storedToken(): string | undefined {
  const token = sessionStorage.getItem(TOKEN_STORAGE_KEY);
  return token !== null && token.length > 0 ? token : undefined;
}

// signOut clears the held token.
export function signOut(): void {
  sessionStorage.removeItem(TOKEN_STORAGE_KEY);
}

// resolveToken produces the bearer token to present to the API on load: the
// OIDC callback exchange when this is a redirect callback, else a token already
// held this session, else the dev-token fallback. undefined means no token —
// the operator must sign in (OIDC) or set a dev token.
export async function resolveToken(config: OidcConfig | undefined): Promise<string | undefined> {
  if (config !== undefined && isAuthCallback()) {
    return completeLogin(config);
  }
  return storedToken() ?? devBearerToken();
}

// devBearerToken is the local-dev / e2e fallback when OIDC is not configured:
// the token from VITE_DEV_BEARER_TOKEN (a desktop-signed or dev token the API
// trusts), or undefined when unset.
export function devBearerToken(): string | undefined {
  return trimmed(import.meta.env.VITE_DEV_BEARER_TOKEN);
}

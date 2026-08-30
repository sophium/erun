// OIDC Authorization Code + PKCE sign-in against the platform issuer, plus the
// dev-token fallback. The console redirects the Operator to the instance's OIDC
// issuer, completes the PKCE code exchange on the callback, and holds the
// resulting JWT to present as `Authorization: Bearer <jwt>` to erun-backend-api
// (whose verifier discovers the issuer's JWKS and checks the signature — see
// api-protocol.md § Sign-in). The id_token is used as the bearer because it is
// always a JWT; an issuer's access tokens may be opaque.
//
// The issuer + client id are resolved at runtime from GET /v1/platform
// (resolveOidcConfig below) — a single built image must be able to serve any
// PaaS instance (#603), so nothing instance-specific can be baked in at build
// time. oidcConfig()'s VITE_* env vars remain only as the local-dev override
// (set them to skip a live backend), and as the fallback resolveOidcConfig uses
// when the discovery endpoint is absent (an older backend) or unreachable. With
// neither source configured the dev token (VITE_DEV_BEARER_TOKEN) applies so
// local dev can still drive the read view with a desktop-signed token.
//
// Driving each env's per-env MCP tools over the live edge
// (mcp.<tenant>-<env>.services.<base-domain>) stays a later increment: it is
// RCE-sensitive (its `raw` tool can `kubectl exec`) and needs a deployed env
// carrying the backend's public key to verify against. Minting the token is
// already implemented (src/mcp/).

import { fetchPlatformConfig } from '../config/platform';

const TOKEN_STORAGE_KEY = 'erun.console.idToken';
const PKCE_VERIFIER_KEY = 'erun.console.pkceVerifier';
const PKCE_STATE_KEY = 'erun.console.oauthState';
const SCOPE_FALLBACK_KEY = 'erun.console.scopeFallback';

// BASE_SCOPE is what every login asks for. ORG_CLAIM_SCOPE is asked for on top,
// so a shared, org-scoped issuer's tokens carry the discriminator erun's tenant
// resolution reads (erun-backend-api's orgClaimValue): without it the API sees
// no org claim and resolves the caller to no tenant at all, however correctly
// they are enrolled. erun-common/cloud_erun.go has requested it by default for
// every CLI login since org-scoping existed; the console was the one client
// that never did, so converting an issuer to org-scoped silently locked it out.
const BASE_SCOPE = 'openid profile email';
const ORG_CLAIM_SCOPE = 'urn:zitadel:iam:user:resourceowner';

// loginScope adds the org-claim scope unless this session already had it
// refused. An issuer that has never heard of it — a dedicated or BYO IdP, the
// common case — rejects the authorize request outright rather than ignoring the
// unknown scope, so login must still work without it. This is the browser-side
// equivalent of erun-common's fallbackScope: asked for by default, dropped once
// on refusal, never retried in a loop.
function loginScope(): string {
  return sessionStorage.getItem(SCOPE_FALLBACK_KEY) === '1'
    ? BASE_SCOPE
    : BASE_SCOPE + ' ' + ORG_CLAIM_SCOPE;
}

export interface OidcConfig {
  issuer: string;
  clientId: string;
  redirectUri: string;
}

// oidcConfig returns the configured OIDC settings, or undefined when the console
// is not wired for OIDC (then the dev-token fallback applies). The redirect URI
// is the SPA's own origin — the callback is handled in place (the App detects
// the ?code= query), so no separate route is needed.
export function oidcConfig(): OidcConfig | undefined {
  const issuer = trimmed(import.meta.env.VITE_OIDC_ISSUER);
  const clientId = trimmed(import.meta.env.VITE_OIDC_CLIENT_ID);
  if (issuer === undefined || clientId === undefined) {
    return undefined;
  }
  return { issuer, clientId, redirectUri: window.location.origin + '/' };
}

// Resolving the OIDC config from platform discovery vs. the VITE_* override —
// the App surfaces `fallbackReason` so an operator debugging a misconfigured
// instance can see why the local-dev values are in play instead of silently
// running against a possibly-wrong client id.
export interface OidcConfigResolution {
  config: OidcConfig | undefined;
  fallbackReason?: string;
}

// resolveOidcConfig fetches GET /v1/platform for the issuer + console client id
// (so one built image serves any PaaS instance), falling back to the VITE_*
// local-dev override when discovery has nothing usable — an older backend
// (404), an unreachable one, or one with the fields unset all resolve to the
// override rather than a hard failure.
export async function resolveOidcConfig(): Promise<OidcConfigResolution> {
  const platform = await fetchPlatformConfig();
  const issuer = trimmed(platform?.issuer);
  const clientId = trimmed(platform?.consoleClientId);
  if (issuer !== undefined && clientId !== undefined) {
    return { config: { issuer, clientId, redirectUri: window.location.origin + '/' } };
  }
  const envConfig = oidcConfig();
  if (envConfig === undefined) {
    return { config: undefined };
  }
  return {
    config: envConfig,
    fallbackReason:
      platform === undefined
        ? 'Platform discovery (GET /v1/platform) is unavailable; using the local VITE_OIDC_* configuration.'
        : 'Platform discovery returned no issuer/console client id; using the local VITE_OIDC_* configuration.',
  };
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
// and a CSRF state (stashed in sessionStorage for the callback), then redirects
// the browser to the issuer's authorization endpoint. `prompt` is the one
// standard OIDC param a caller may add on top of the baseline request —
// `select_account` is what the tenant switcher (shell/tenantSwitch.ts) passes
// so the IdP offers an account/org picker instead of silently reusing
// whatever session it already holds, which would make "switch" a no-op for a
// caller who is still signed into the browser as the same identity.
export async function beginLogin(config: OidcConfig, prompt?: string): Promise<void> {
  const discovery = await discover(config.issuer);
  const verifier = randomString();
  const state = randomString();
  sessionStorage.setItem(PKCE_VERIFIER_KEY, verifier);
  sessionStorage.setItem(PKCE_STATE_KEY, state);
  const params = new URLSearchParams({
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    response_type: 'code',
    scope: loginScope(),
    code_challenge: await pkceChallenge(verifier),
    code_challenge_method: 'S256',
    state,
  });
  if (prompt !== undefined && prompt.length > 0) {
    params.set('prompt', prompt);
  }
  window.location.assign(discovery.authorization_endpoint + '?' + params.toString());
}

// isAuthCallback reports whether the current URL is an OIDC redirect callback
// (carries ?code=&state=).
export function isAuthCallback(): boolean {
  const params = new URLSearchParams(window.location.search);
  return params.has('code') && params.has('state');
}

// completeLogin finishes the callback: it validates the state, exchanges the
// code + PKCE verifier for tokens, stores the id_token, clears the one-shot PKCE
// state, and strips the query from the URL so a reload is not a replay.
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
    typeof body === 'object' && body !== null
      ? (body as { id_token?: unknown }).id_token
      : undefined;
  if (typeof idToken !== 'string' || idToken.length === 0) {
    throw new Error('OIDC token response had no id_token');
  }
  sessionStorage.setItem(TOKEN_STORAGE_KEY, idToken);
  return idToken;
}

function cleanCallbackUrl(): void {
  window.history.replaceState(
    {},
    document.title,
    window.location.origin + window.location.pathname,
  );
}

// storedToken returns a previously-obtained id_token from this browser session.
export function storedToken(): string | undefined {
  const token = sessionStorage.getItem(TOKEN_STORAGE_KEY);
  return token !== null && token.length > 0 ? token : undefined;
}

// signOut clears the held token.
export function signOut(): void {
  sessionStorage.removeItem(TOKEN_STORAGE_KEY);
}

// authCallbackError returns the OIDC error code when the redirect came back as
// a failure instead of a code. Distinct from isAuthCallback, which only reports
// the success shape (?code=&state=) — an error redirect carries neither, so
// without this the failure is silently indistinguishable from never having
// signed in.
export function authCallbackError(): string | undefined {
  const params = new URLSearchParams(window.location.search);
  const error = params.get('error');
  return error !== null && error.length > 0 ? error : undefined;
}

// isOrgScopeRefusal reports whether a failed callback is the issuer rejecting
// the org-claim scope. Issuers differ on which code they use for an unknown
// scope, so both spellings the spec allows are treated as a refusal.
function isOrgScopeRefusal(error: string): boolean {
  return error === 'invalid_scope' || error === 'invalid_request';
}

// retryLoginWithoutOrgScope arms the fallback and restarts the flow, once. The
// marker lives in sessionStorage, so a second refusal in the same session
// cannot loop, and a new session asks for the scope again — an issuer that
// gains org-claim support is picked up without anyone clearing state by hand.
async function retryLoginWithoutOrgScope(config: OidcConfig): Promise<void> {
  sessionStorage.setItem(SCOPE_FALLBACK_KEY, '1');
  await beginLogin(config);
}

// resolveToken produces the bearer token to present to the API on load: the OIDC
// callback exchange when this is a redirect callback, else a token already held
// this session, else the dev-token fallback. undefined means no token — the
// operator must sign in (OIDC) or set a dev token.
export async function resolveToken(config: OidcConfig | undefined): Promise<string | undefined> {
  if (config !== undefined && isAuthCallback()) {
    return completeLogin(config);
  }
  const callbackError = authCallbackError();
  if (
    config !== undefined &&
    callbackError !== undefined &&
    isOrgScopeRefusal(callbackError) &&
    sessionStorage.getItem(SCOPE_FALLBACK_KEY) !== '1'
  ) {
    await retryLoginWithoutOrgScope(config);
    return undefined;
  }
  return storedToken() ?? devBearerToken();
}

// devBearerToken is the local-dev fallback when OIDC is not configured: the
// token from VITE_DEV_BEARER_TOKEN (a desktop-signed or dev token the API
// trusts), or undefined when unset.
export function devBearerToken(): string | undefined {
  return trimmed(import.meta.env.VITE_DEV_BEARER_TOKEN);
}

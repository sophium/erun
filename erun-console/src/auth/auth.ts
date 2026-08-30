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
//
// signOut performs RP-initiated logout (OIDC Session Management) rather than
// clearing the local token alone: the IdP's session otherwise outlives the
// console's, so the next sign-in is silently re-authenticated as the same
// account with no way to reach a different one. Not every IdP advertises
// end_session_endpoint, so callers that state what sign-out will do (the
// not-enrolled screen) check endSessionSupported first rather than assume it.
// The redirect target (post_logout_redirect_uri, this origin) must be
// registered on the IdP client the same way redirect_uri already is.

import { fetchPlatformConfig } from '../config/platform';

const TOKEN_STORAGE_KEY = 'erun.console.idToken';
const PKCE_VERIFIER_KEY = 'erun.console.pkceVerifier';
const PKCE_STATE_KEY = 'erun.console.oauthState';
const ORG_SCOPE_RETRIED_KEY = 'erun.console.oidcOrgScopeRetried';

// ORG_CLAIM_SCOPE asks the shipped Zitadel IdP to include the org
// (resourceowner) claim erun's tenant resolution reads for a shared,
// org-scoped issuer -- the same scope erun-common's CLI/desktop OIDC login
// requests by default (see erun-common/cloud_erun_oidc.go's
// erunOrgClaimScope). Without it, a console session's token carries no org
// claim, so once an issuer is org-scoped, the console cannot resolve its
// tenant even for an already-enrolled operator (erun#1721). A dedicated/BYO
// issuer has never heard of this scope; beginLogin retries once without it
// (see resolveToken's authCallbackError handling) when the issuer redirects
// back with error=invalid_scope, so sign-in still succeeds exactly as it did
// before this default was added.
const ORG_CLAIM_SCOPE = 'urn:zitadel:iam:user:resourceowner';

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
  // Not every IdP advertises RP-initiated logout (OIDC Session Management is
  // an optional extension), so this is the one discovery field callers must
  // treat as absent rather than assume present.
  end_session_endpoint?: string;
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
  const record = doc as Record<string, unknown>;
  return {
    authorization_endpoint: record.authorization_endpoint as string,
    token_endpoint: record.token_endpoint as string,
    end_session_endpoint:
      typeof record.end_session_endpoint === 'string' ? record.end_session_endpoint : undefined,
  };
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
// `includeOrgScope` defaults to true; resolveToken passes false on the
// one-shot retry after an issuer refuses ORG_CLAIM_SCOPE.
export async function beginLogin(
  config: OidcConfig,
  prompt?: string,
  includeOrgScope = true,
): Promise<void> {
  const discovery = await discover(config.issuer);
  const verifier = randomString();
  const state = randomString();
  sessionStorage.setItem(PKCE_VERIFIER_KEY, verifier);
  sessionStorage.setItem(PKCE_STATE_KEY, state);
  const scope = includeOrgScope
    ? `openid profile email ${ORG_CLAIM_SCOPE}`
    : 'openid profile email';
  const params = new URLSearchParams({
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    response_type: 'code',
    scope,
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

// authCallbackError reads the OIDC error code from a failed redirect (the
// issuer sends ?error=...&error_description=... instead of ?code=&state=
// when it refuses the request).
function authCallbackError(): string | undefined {
  const params = new URLSearchParams(window.location.search);
  return params.get('error') ?? undefined;
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

export interface SignOutResult {
  // Whether this call redirected the browser to the IdP's end_session_endpoint
  // to end its session there too. false means only the local token was
  // cleared — no OIDC config, no discovered end_session_endpoint, or
  // discovery failed — so the caller (whose UI made a claim about what
  // sign-out does) must not treat this as "the IdP session ended" silently.
  idpSessionEnded: boolean;
}

// signOut always clears the locally-held token first, then attempts
// RP-initiated logout: it redirects to the IdP's discovered
// end_session_endpoint with id_token_hint (the id_token this session held, so
// the IdP knows which session to end without prompting) and
// post_logout_redirect_uri back to this origin. Not every IdP advertises
// end_session_endpoint (it's an optional OIDC extension), and discovery
// itself can fail (network, misconfigured issuer) — either case falls back
// to the local-only clear rather than throwing, since a failed logout
// redirect must never leave the caller signed in.
//
// It also clears ORG_SCOPE_RETRIED_KEY: that flag exists to bound a single
// sign-in attempt to one retry, not to survive past a sign-out. Leaving it
// set would mean a dedicated/BYO issuer that already consumed its one retry
// gets no retry on the next sign-in after sign-out either — resolveToken
// would treat the resulting invalid_scope callback as "no token" and bounce
// back to the sign-in screen with no explanation, forever.
export async function signOut(config: OidcConfig | undefined): Promise<SignOutResult> {
  const idToken = storedToken();
  sessionStorage.removeItem(TOKEN_STORAGE_KEY);
  sessionStorage.removeItem(ORG_SCOPE_RETRIED_KEY);
  if (config === undefined) {
    return { idpSessionEnded: false };
  }
  const endSessionEndpoint = await discoverEndSessionEndpoint(config.issuer);
  if (endSessionEndpoint === undefined) {
    return { idpSessionEnded: false };
  }
  const params = new URLSearchParams({ post_logout_redirect_uri: config.redirectUri });
  if (idToken !== undefined) {
    params.set('id_token_hint', idToken);
  }
  window.location.assign(endSessionEndpoint + '?' + params.toString());
  return { idpSessionEnded: true };
}

async function discoverEndSessionEndpoint(issuer: string): Promise<string | undefined> {
  try {
    return (await discover(issuer)).end_session_endpoint;
  } catch {
    return undefined;
  }
}

// endSessionSupported reports whether signOut(config) will be able to end the
// IdP session, without performing sign-out. A screen that advises "sign out
// to use a different account" must know this before the click — advising a
// remedy discovery cannot back up is the same dead end as a wrong error
// message (see root AGENTS.md § "Smooth, Seamless, No Dead Ends").
export async function endSessionSupported(config: OidcConfig | undefined): Promise<boolean> {
  if (config === undefined) {
    return false;
  }
  return (await discoverEndSessionEndpoint(config.issuer)) !== undefined;
}

// resolveToken produces the bearer token to present to the API on load: the OIDC
// callback exchange when this is a redirect callback, else a token already held
// this session, else the dev-token fallback. undefined means no token — the
// operator must sign in (OIDC) or set a dev token.
//
// A callback that failed with error=invalid_scope means the issuer refused
// ORG_CLAIM_SCOPE (a dedicated/BYO IdP that has never heard of it) — this
// retries sign-in once without that scope, the same one-shot fallback
// erun-common's CLI/desktop login already does, so a dedicated issuer signs
// in exactly as it did before the org-scope default was added. The
// sessionStorage flag bounds the retry to once per browser session.
export async function resolveToken(config: OidcConfig | undefined): Promise<string | undefined> {
  if (config !== undefined && isAuthCallback()) {
    return completeLogin(config);
  }
  if (
    config !== undefined &&
    authCallbackError() === 'invalid_scope' &&
    sessionStorage.getItem(ORG_SCOPE_RETRIED_KEY) === null
  ) {
    sessionStorage.setItem(ORG_SCOPE_RETRIED_KEY, '1');
    cleanCallbackUrl();
    await beginLogin(config, undefined, false);
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

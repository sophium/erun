// OIDC Authorization Code + PKCE sign-in, plus the dev-token fallback. The id_token
// is the bearer because it is always a JWT; access tokens may be opaque. Issuer and
// client id come from GET /v1/platform at runtime so one image serves any instance.

import { fetchPlatformConfig } from '../config/platform';

const TOKEN_STORAGE_KEY = 'erun.console.idToken';
const PKCE_VERIFIER_KEY = 'erun.console.pkceVerifier';
const PKCE_STATE_KEY = 'erun.console.oauthState';
const ORG_SCOPE_RETRIED_KEY = 'erun.console.oidcOrgScopeRetried';

// ORG_CLAIM_SCOPE asks Zitadel for the org claim tenant resolution needs on a shared
// issuer. Issuers that reject it (invalid_scope) get one retry without it.
const ORG_CLAIM_SCOPE = 'urn:zitadel:iam:user:resourceowner';

export interface OidcConfig {
  issuer: string;
  clientId: string;
  redirectUri: string;
}

// The callback is handled in place at the SPA origin, so no separate route is needed.
export function oidcConfig(): OidcConfig | undefined {
  const issuer = trimmed(import.meta.env.VITE_OIDC_ISSUER);
  const clientId = trimmed(import.meta.env.VITE_OIDC_CLIENT_ID);
  if (issuer === undefined || clientId === undefined) {
    return undefined;
  }
  return { issuer, clientId, redirectUri: window.location.origin + '/' };
}

// `fallbackReason` explains why local-dev values are in play.
export interface OidcConfigResolution {
  config: OidcConfig | undefined;
  fallbackReason?: string;
}

// resolveOidcConfig falls back to the VITE_* override when discovery has nothing usable.
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
  // Optional: RP-initiated logout is an OIDC extension not every IdP supports.
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

// `select_account` makes the IdP offer an account picker so a tenant switch is not a
// silent reuse of the current session.
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

export function isAuthCallback(): boolean {
  const params = new URLSearchParams(window.location.search);
  return params.has('code') && params.has('state');
}

function authCallbackError(): string | undefined {
  const params = new URLSearchParams(window.location.search);
  return params.get('error') ?? undefined;
}

// Strips the query from the URL so a reload is not a replay.
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

export function storedToken(): string | undefined {
  const token = sessionStorage.getItem(TOKEN_STORAGE_KEY);
  return token !== null && token.length > 0 ? token : undefined;
}

export interface SignOutResult {
  // false means only the local token was cleared.
  idpSessionEnded: boolean;
}

// signOut clears the local token first so a failed IdP redirect never leaves the caller
// signed in. It also clears ORG_SCOPE_RETRIED_KEY, which bounds one sign-in attempt,
// not the session.
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

// endSessionSupported lets a screen avoid advising a sign-out remedy the IdP cannot back.
export async function endSessionSupported(config: OidcConfig | undefined): Promise<boolean> {
  if (config === undefined) {
    return false;
  }
  return (await discoverEndSessionEndpoint(config.issuer)) !== undefined;
}

// resolveToken returns undefined when the operator must sign in. An invalid_scope
// callback retries once without ORG_CLAIM_SCOPE, bounded by a sessionStorage flag.
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

export function devBearerToken(): string | undefined {
  return trimmed(import.meta.env.VITE_DEV_BEARER_TOKEN);
}

/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Empty/unset means same-origin: the console is served behind the auth edge that fronts the API.
  readonly VITE_API_BASE?: string;
  // OIDC Authorization Code + PKCE sign-in (src/auth/auth.ts). VITE_OIDC_ISSUER
  // is the platform issuer (e.g. a Zitadel instance); VITE_OIDC_CLIENT_ID is the
  // console's public SPA client. Both set → the console runs the real sign-in
  // flow; unset → the dev-token fallback below applies. Per-instance config,
  // never hardcoded.
  readonly VITE_OIDC_ISSUER?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  // Local-dev fallback when OIDC is not configured: a token the API trusts (a
  // desktop-signed file:// token, or an OIDC JWT). Never a production auth path.
  readonly VITE_DEV_BEARER_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

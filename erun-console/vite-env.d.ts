/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Empty/unset means same-origin: the console is served behind the auth edge that fronts the API.
  readonly VITE_API_BASE?: string;
  // OIDC Authorization Code + PKCE sign-in (src/auth/auth.ts). The console
  // resolves the issuer + console client id at runtime from GET /v1/platform so
  // one built image serves any PaaS instance; VITE_OIDC_ISSUER/VITE_OIDC_CLIENT_ID
  // are a local-dev override only, used when that endpoint is absent (an older
  // backend) or unreachable. Neither source configured → the dev-token fallback
  // below applies.
  readonly VITE_OIDC_ISSUER?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  // Local-dev fallback when OIDC is not configured: a token the API trusts (a
  // desktop-signed file:// token, or an OIDC JWT). Never a production auth path.
  readonly VITE_DEV_BEARER_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

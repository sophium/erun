/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Empty/unset means same-origin: the console is served behind the auth edge that fronts the API.
  readonly VITE_API_BASE?: string;
  // Local-dev override; the console normally resolves these from GET /v1/platform.
  readonly VITE_OIDC_ISSUER?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  // Local-dev fallback when OIDC is not configured; never a production auth path.
  readonly VITE_DEV_BEARER_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

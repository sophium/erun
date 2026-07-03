/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Base URL of the erun-backend-api the console reads its config from.
  // Defaults to a same-origin '' when unset (the SPA is served behind the
  // auth edge that fronts the API).
  readonly VITE_API_BASE?: string;
  // TODO: a real OIDC Authorization Code + PKCE flow replaces this.
  // Until the live Zitadel issuer exists to verify against, the read view is
  // exercised with a dev token injected here. Never a production auth path.
  readonly VITE_DEV_BEARER_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

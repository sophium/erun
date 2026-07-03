/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Empty/unset means same-origin: the console is served behind the auth edge that fronts the API.
  readonly VITE_API_BASE?: string;
  // Dev-only stub token exercising the read view until a real OIDC flow exists. Never a production auth path.
  readonly VITE_DEV_BEARER_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

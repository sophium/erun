// The console's brand, resolved from one source.
//
// `platform.brand` — this deployment's `platform.brand`, served unauthenticated
// at GET /v1/platform — is the instance's authoritative display name. One
// console image serves any instance, so that name cannot be baked into the
// bundle, and the console must not carry a second name of its own either: a
// pre-resolution paint under a different name is a visible change of identity
// on every load.
//
// So the served page carries the deployed value as
// `window.__ERUN_PLATFORM_BRAND__`, injected from the same config key by the
// image's own entrypoint (see
// erun-devops/docker/erun-console/docker-entrypoint.d/05-platform-brand.sh) and
// mirrored in development by vite.config.ts's brand plugin. Every pre-shell
// screen and the document title read it here, so the first paint already shows
// what discovery will resolve to.
//
// BUNDLED_BRAND is the product-level name an instance that names itself nowhere
// shows. It is not a competing identity: in that state the server serves an
// empty `brand` too, so both sides land on this same value.

/** The product-level name for an instance that configures no brand. */
export const BUNDLED_BRAND = 'ERun';

declare global {
  interface Window {
    /** Injected into the served page from the deploy's `platform.brand`. */
    __ERUN_PLATFORM_BRAND__?: string;
  }
}

function injectedBrand(): string | undefined {
  const injected = window.__ERUN_PLATFORM_BRAND__;
  return typeof injected === 'string' && injected.length > 0 ? injected : undefined;
}

// consoleBrand is the name every pre-shell screen renders. The server's own
// value wins once discovery answers — it is what the instance is configured to
// be — and until then the value the deploy injected into the page stands in.
// Both come from the same `platform.brand`, so resolving does not change what
// is already on screen.
export function consoleBrand(platformBrand: string | undefined): string {
  if (platformBrand !== undefined && platformBrand.length > 0) {
    return platformBrand;
  }
  return injectedBrand() ?? BUNDLED_BRAND;
}

// consoleBrandLabel is the document-title form, matching what the served
// index.html carries before the bundle runs.
export function consoleBrandLabel(platformBrand: string | undefined): string {
  return `${consoleBrand(platformBrand)} console`;
}

// `platform.brand` is the instance's display name; the deploy injects it as
// window.__ERUN_PLATFORM_BRAND__ so the first paint matches discovery.

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

// consoleBrand prefers the server's value, falling back to the injected one.
export function consoleBrand(platformBrand: string | undefined): string {
  if (platformBrand !== undefined && platformBrand.length > 0) {
    return platformBrand;
  }
  return injectedBrand() ?? BUNDLED_BRAND;
}

// consoleBrandLabel is the document-title form.
export function consoleBrandLabel(platformBrand: string | undefined): string {
  return `${consoleBrand(platformBrand)} console`;
}

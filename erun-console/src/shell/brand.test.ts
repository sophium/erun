import { afterEach, describe, expect, it } from 'vitest';

import bootPage from '../../index.html?raw';
import { BUNDLED_BRAND, consoleBrand, consoleBrandLabel } from './brand';

// These assert on the two values themselves — the name the served page carries
// before discovery resolves and the name the server supplies — rather than that
// some brand renders. A "a brand is present" assertion passes on exactly the
// version this guards, where the page painted a name of its own and the
// resolved brand replaced it in front of the user on every load.
describe('console brand', () => {
  afterEach(() => {
    delete window.__ERUN_PLATFORM_BRAND__;
  });

  it('paints the brand the server supplies, so discovery resolves to what is already on screen', () => {
    // What the deploy injects into the page from platform.brand...
    window.__ERUN_PLATFORM_BRAND__ = 'ErunPaaS';
    // ...is the same value GET /v1/platform serves for that key.
    const servedByServer = 'ErunPaaS';

    expect(consoleBrand(undefined)).toBe(servedByServer);
    expect(consoleBrand(servedByServer)).toBe(servedByServer);
  });

  it('agrees on the bundled default when the instance names itself nowhere', () => {
    // No injection and an empty server brand are the same state: an instance
    // that configures no brand. Both sides land on the bundled default rather
    // than one showing a second product name.
    expect(consoleBrand(undefined)).toBe(BUNDLED_BRAND);
    expect(consoleBrand('')).toBe(BUNDLED_BRAND);
  });

  it('ships that same name in the boot page, so the pre-bundle title does not flip either', () => {
    // The container entrypoint rewrites this title from platform.brand at start
    // (erun-devops/docker/erun-console/docker-entrypoint.d/05-platform-brand.sh),
    // and the bundle reads the same value for its pre-resolution paint. What
    // this pins is the state that rewrite leaves alone: an instance that names
    // itself nowhere must still show one name on both sides, not the bundled
    // product default here and something else there.
    expect(bootPage).toContain(`<title>${consoleBrandLabel(undefined)}</title>`);
  });
});

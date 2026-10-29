import { afterEach, describe, expect, it } from 'vitest';

import bootPage from '../../index.html?raw';
import { BUNDLED_BRAND, consoleBrand, consoleBrandLabel } from './brand';

describe('console brand', () => {
  afterEach(() => {
    delete window.__ERUN_PLATFORM_BRAND__;
  });

  it('paints the brand the server supplies, so discovery resolves to what is already on screen', () => {
    window.__ERUN_PLATFORM_BRAND__ = 'ErunPaaS';
    const servedByServer = 'ErunPaaS';

    expect(consoleBrand(undefined)).toBe(servedByServer);
    expect(consoleBrand(servedByServer)).toBe(servedByServer);
  });

  it('agrees on the bundled default when the instance names itself nowhere', () => {
    expect(consoleBrand(undefined)).toBe(BUNDLED_BRAND);
    expect(consoleBrand('')).toBe(BUNDLED_BRAND);
  });

  it('ships that same name in the boot page, so the pre-bundle title does not flip either', () => {
    // The entrypoint rewrites this title from platform.brand; unset, both sides
    // must agree on one name.
    expect(bootPage).toContain(`<title>${consoleBrandLabel(undefined)}</title>`);
  });
});

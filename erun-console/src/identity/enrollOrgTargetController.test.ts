import { describe, expect, it } from 'vitest';

import { resolveOrgTarget } from './enrollOrgTargetController';

describe('resolveOrgTarget', () => {
  it('defaults to the platform’s own org when nothing else is chosen', () => {
    expect(resolveOrgTarget(true, false, undefined, [])).toEqual({ status: 'default' });
  });

  it('reports loading while the target tenant’s issuer mapping is in flight', () => {
    expect(resolveOrgTarget(false, true, undefined, [])).toEqual({ status: 'loading' });
  });

  it('distinguishes a lookup failure from a tenant with no org mapping', () => {
    expect(resolveOrgTarget(false, false, { status: 502, message: 'bad gateway' }, [])).toEqual({
      status: 'error',
      message: 'bad gateway',
    });
    expect(resolveOrgTarget(false, false, undefined, [])).toEqual({ status: 'unmapped' });
    expect(resolveOrgTarget(false, false, undefined, [undefined, ''])).toEqual({
      status: 'unmapped',
    });
  });

  it('resolves to the first non-empty org value once the mapping is read', () => {
    expect(resolveOrgTarget(false, false, undefined, [undefined, '', '999'])).toEqual({
      status: 'resolved',
      orgId: '999',
    });
  });
});

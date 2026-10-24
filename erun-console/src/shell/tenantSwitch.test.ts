import { afterEach, describe, expect, it } from 'vitest';

import { beginTenantSwitch, consumeTenantSwitchIntent } from './tenantSwitch';

afterEach(() => {
  sessionStorage.clear();
});

describe('tenantSwitch', () => {
  it('round-trips a recorded target exactly once', () => {
    beginTenantSwitch({ tenantId: 'tenant-b', name: 'Beta' });

    expect(consumeTenantSwitchIntent()).toEqual({ tenantId: 'tenant-b', name: 'Beta' });
    expect(consumeTenantSwitchIntent()).toBeUndefined();
  });

  it('reports no pending target when none was recorded', () => {
    expect(consumeTenantSwitchIntent()).toBeUndefined();
  });

  it('ignores malformed stored state rather than throwing', () => {
    sessionStorage.setItem('erun.console.tenantSwitchTarget', '{not json');
    expect(consumeTenantSwitchIntent()).toBeUndefined();
  });

  it('ignores a stored value missing the expected shape', () => {
    sessionStorage.setItem('erun.console.tenantSwitchTarget', JSON.stringify({ tenantId: 'x' }));
    expect(consumeTenantSwitchIntent()).toBeUndefined();
  });
});

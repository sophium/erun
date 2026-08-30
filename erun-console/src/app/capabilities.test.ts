import { describe, expect, it } from 'vitest';

import { capabilityAllows } from './capabilities';

const APPROVE_TEMPLATE = '/v1/invite-requests/{invite_request_id}/approve';

describe('capabilityAllows', () => {
  it('matches an exact canonical route template', () => {
    expect(
      capabilityAllows([{ method: 'POST', path: APPROVE_TEMPLATE }], 'POST', APPROVE_TEMPLATE),
    ).toBe(true);
  });

  it('matches a concrete path against the template wildcard segment', () => {
    expect(
      capabilityAllows(
        [{ method: 'POST', path: APPROVE_TEMPLATE }],
        'POST',
        '/v1/invite-requests/abc-123/approve',
      ),
    ).toBe(true);
  });

  it('refuses a method mismatch', () => {
    expect(
      capabilityAllows([{ method: 'GET', path: APPROVE_TEMPLATE }], 'POST', APPROVE_TEMPLATE),
    ).toBe(false);
  });

  it('refuses when no capability names the path at all', () => {
    expect(
      capabilityAllows([{ method: 'GET', path: '/v1/invite-requests' }], 'POST', APPROVE_TEMPLATE),
    ).toBe(false);
  });

  it('treats an unresolved capability set as false, not a match', () => {
    expect(capabilityAllows(undefined, 'POST', APPROVE_TEMPLATE)).toBe(false);
  });
});

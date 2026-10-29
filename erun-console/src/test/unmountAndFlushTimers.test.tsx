import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ScopeSelector } from '../shell/ScopeSelector';
import { unmountAndFlushTimers } from './setup';

vi.mock('../auth/auth', () => ({
  beginLogin: vi.fn(),
}));

const OWN_TENANT = { tenantId: 'tenant-own', name: 'Acme' };

const TENANTS = [
  { tenantId: 'tenant-own', name: 'Acme', type: 'OPERATIONS', createdAt: '', updatedAt: '' },
  { tenantId: 'tenant-other', name: 'Beta', type: 'COMPANY', createdAt: '', updatedAt: '' },
];

// The scope's unmount event must have fired by the time the shared teardown returns,
// or a file that ends right after it leaves the event to a torn-down jsdom.
describe('the shared test teardown', () => {
  it('lets an open Radix focus scope dispatch its unmount event before returning', async () => {
    render(
      <ScopeSelector
        tenantType="OPERATIONS"
        tenants={TENANTS}
        ownTenant={OWN_TENANT}
        value={undefined}
        active="overview"
        onChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('combobox'));
    await screen.findByRole('option', { name: 'Beta' });

    const dispatchEvent = vi.spyOn(EventTarget.prototype, 'dispatchEvent');
    let dispatched: string[];
    try {
      await unmountAndFlushTimers();
      dispatched = dispatchEvent.mock.calls.map(([event]) => event.type);
    } finally {
      dispatchEvent.mockRestore();
    }

    expect(dispatched).toContain('focusScope.autoFocusOnUnmount');
  });
});

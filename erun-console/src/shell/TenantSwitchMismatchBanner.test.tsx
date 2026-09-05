import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { TenantSwitchMismatchBanner } from './TenantSwitchMismatchBanner';

const MISMATCH = {
  requestedTenantId: 'tenant-b',
  requestedName: 'Beta',
  resolvedName: 'Acme',
  resolvedType: 'COMPANY',
};

afterEach(() => {
  cleanup();
});

describe('TenantSwitchMismatchBanner', () => {
  it('names both the requested and the resolved tenant, and offers retry + dismiss', () => {
    const onRetry = vi.fn();
    const onDismiss = vi.fn();
    render(
      <TenantSwitchMismatchBanner mismatch={MISMATCH} onRetry={onRetry} onDismiss={onDismiss} />,
    );

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Beta');
    expect(alert).toHaveTextContent('Acme');
    expect(alert).toHaveTextContent('COMPANY');

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  // No retry is offered with nothing to retry against (the dev-token
  // fallback, which has no OIDC config to redirect through) — a dead-end
  // button would be worse than no button.
  it('omits the retry action when none is given', () => {
    render(<TenantSwitchMismatchBanner mismatch={MISMATCH} onDismiss={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument();
  });
});

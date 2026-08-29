import { Button } from 'erun-kit';
import type * as React from 'react';

export interface TenantSwitchMismatch {
  requestedTenantId: string;
  requestedName: string;
  resolvedName: string;
  resolvedType: string;
}

// TenantSwitchMismatchBanner is what an unsuccessful switch looks like: the
// console never keeps the old token and relabels which tenant it claims to be
// on, so when a fresh sign-in comes back resolving to a tenant other than the
// one requested, it says so plainly instead of silently proceeding as if the
// switch had worked. `onRetry` is omitted when there is no OIDC config to
// retry against (the dev-token fallback), in which case only dismissing is
// offered.
export function TenantSwitchMismatchBanner({
  mismatch,
  onRetry,
  onDismiss,
}: {
  mismatch: TenantSwitchMismatch;
  onRetry?: () => void;
  onDismiss: () => void;
}): React.ReactElement {
  return (
    <div
      role="alert"
      className="flex flex-none flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/50 px-6 py-2.5 text-sm"
    >
      <p className="text-foreground">
        You asked to switch to <strong>{mismatch.requestedName}</strong>, but signed back in as{' '}
        <strong>{mismatch.resolvedName}</strong> ({mismatch.resolvedType}). Sign in with an identity
        that belongs to {mismatch.requestedName} to reach it.
      </p>
      <div className="flex flex-none items-center gap-2">
        {onRetry !== undefined && (
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        )}
        <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
          Dismiss
        </Button>
      </div>
    </div>
  );
}

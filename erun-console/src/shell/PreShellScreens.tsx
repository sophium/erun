import { LoaderCircle } from 'lucide-react';
import type * as React from 'react';

import { readTokenIdentity } from '../auth/identity';
import { CenteredCard } from './CenteredCard';

export function LoadingScreen({ brand }: { brand: string | undefined }): React.ReactElement {
  return (
    <CenteredCard brand={brand} title="Loading your environments…" role="status">
      <LoaderCircle
        aria-hidden="true"
        className="mx-auto size-5 animate-spin text-muted-foreground"
      />
    </CenteredCard>
  );
}

// NotEnrolledScreen is the dead end a self-signed-up user hits: OIDC
// succeeded, so a token is held, but the API rejects the identity because it
// is enrolled in no tenant. This says what actually has to happen (an
// operator must enrol them) and names the identity an operator needs in order
// to do it — signing in again just returns here (#1167).
export function NotEnrolledScreen({
  brand,
  token,
}: {
  brand: string | undefined;
  token: string;
}): React.ReactElement {
  const identity = readTokenIdentity(token);
  const named = identity.email ?? identity.subject;
  return (
    <CenteredCard brand={brand} title="Not yet part of a tenant" role="status">
      <p className="text-sm text-muted-foreground">
        You are signed in, but your account is not yet part of a tenant on this platform.
      </p>
      <p className="text-sm text-muted-foreground">
        An operator has to enrol you before you can see any environments. Signing in again will not
        change this.
      </p>
      {named !== undefined && (
        <p className="text-xs text-muted-foreground">
          Give them this identity: {named}
          {identity.email !== undefined && identity.subject !== undefined
            ? ` (subject ${identity.subject})`
            : ''}
          {identity.issuer !== undefined ? ` from ${identity.issuer}` : ''}
        </p>
      )}
    </CenteredCard>
  );
}

export function ErrorScreen({
  brand,
  message,
}: {
  brand: string | undefined;
  message: string;
}): React.ReactElement {
  return (
    <CenteredCard brand={brand} title="Could not load your environments" role="alert">
      <p className="text-sm text-destructive">{message}</p>
    </CenteredCard>
  );
}

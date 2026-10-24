import { Button } from 'erun-kit';
import { Copy, LoaderCircle, LogOut } from 'lucide-react';
import * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { endSessionSupported } from '../auth/auth';
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

// useCanEndIdpSession defaults to false and only upgrades once discovery
// confirms an end_session_endpoint, so the copy never retracts a promise.
function useCanEndIdpSession(oidc: OidcConfig | undefined): boolean {
  const [supported, setSupported] = React.useState(false);
  React.useEffect(() => {
    let cancelled = false;
    endSessionSupported(oidc)
      .then((result) => {
        if (!cancelled) {
          setSupported(result);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [oidc]);
  return supported;
}

function enrollCommand(identity: ReturnType<typeof readTokenIdentity>): string {
  const username = identity.email ?? '<username>';
  const issuer = identity.issuer ?? '<issuer>';
  const subject = identity.subject ?? '<subject>';
  return `erun platform user enroll --username ${username} --issuer ${issuer} --subject ${subject}`;
}

function CopyCommandButton({ command }: { command: string }): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={() => {
        void navigator.clipboard.writeText(command).then(() => {
          setCopied(true);
        });
      }}
    >
      <Copy aria-hidden="true" />
      {copied ? 'Copied' : 'Copy'}
    </Button>
  );
}

// NotEnrolledScreen: the token is valid but the identity is enrolled in no
// tenant. Signing in as a different identity is the escape.
export function NotEnrolledScreen({
  brand,
  token,
  oidc,
  onSignOut,
}: {
  brand: string | undefined;
  token: string;
  oidc: OidcConfig | undefined;
  onSignOut: () => void;
}): React.ReactElement {
  const identity = readTokenIdentity(token);
  const named = identity.email ?? identity.subject;
  const canEndIdpSession = useCanEndIdpSession(oidc);
  return (
    <CenteredCard brand={brand} title="Not yet part of a tenant" role="status">
      <p className="text-sm text-muted-foreground">
        You are signed in, but your account is not yet part of a tenant on this platform.
      </p>
      <p className="text-sm text-muted-foreground">
        {canEndIdpSession ? (
          <>
            An operator has to enrol you before you can see any environments. Signing in again as
            this same account will not change that. Sign out below — it also ends your identity
            provider session, so signing in again lets you choose a different one.
          </>
        ) : (
          <>
            An operator has to enrol you before you can see any environments. Signing in again as
            this same account will not change that, and sign-out here only clears this
            browser&apos;s session — it cannot end your identity provider&apos;s session from here.
            To use a different account, sign out of your identity provider directly, then sign in
            here again.
          </>
        )}
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
      <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/30 p-3 text-left">
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-xs font-medium text-muted-foreground">
            Or ask an administrator to run
          </span>
          <CopyCommandButton command={enrollCommand(identity)} />
        </div>
        <code className="block overflow-x-hidden whitespace-pre-wrap break-words rounded bg-background px-2 py-1.5 text-[12px]">
          {enrollCommand(identity)}
        </code>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="justify-self-start"
        onClick={onSignOut}
      >
        <LogOut aria-hidden="true" />
        Sign out
      </Button>
    </CenteredCard>
  );
}

// TenantUnresolvedScreen: the issuer could not be mapped to a tenant. It must
// not tell the caller to get enrolled; they may already be.
export function TenantUnresolvedScreen({
  brand,
  token,
  message,
  onSignOut,
}: {
  brand: string | undefined;
  token: string;
  message: string;
  onSignOut: () => void;
}): React.ReactElement {
  const identity = readTokenIdentity(token);
  const named = identity.email ?? identity.subject;
  return (
    <CenteredCard brand={brand} title="Could not determine your tenant" role="alert">
      <p className="text-sm text-muted-foreground">
        You signed in successfully, but this platform could not tell which tenant your account
        belongs to. This is not the same as not being enrolled — you may already be a member of a
        tenant here.
      </p>
      <p className="text-sm text-destructive">{message}</p>
      {named !== undefined && (
        <p className="text-xs text-muted-foreground">
          Identity: {named}
          {identity.email !== undefined && identity.subject !== undefined
            ? ` (subject ${identity.subject})`
            : ''}
          {identity.issuer !== undefined ? ` from ${identity.issuer}` : ''}
        </p>
      )}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="justify-self-start"
        onClick={onSignOut}
      >
        <LogOut aria-hidden="true" />
        Sign out
      </Button>
    </CenteredCard>
  );
}

// ResolutionErrorScreen: resolution failed internally, so it offers no
// enrolment command.
export function ResolutionErrorScreen({
  brand,
  token,
  message,
  onSignOut,
}: {
  brand: string | undefined;
  token: string;
  message: string;
  onSignOut: () => void;
}): React.ReactElement {
  const identity = readTokenIdentity(token);
  const named = identity.email ?? identity.subject;
  return (
    <CenteredCard brand={brand} title="Could not resolve your identity" role="alert">
      <p className="text-sm text-muted-foreground">
        You signed in successfully, but this platform hit an internal error while resolving your
        identity. This is not the same as not being enrolled, and not the same as your tenant being
        unclear — something went wrong on this platform&apos;s side. Try signing in again in a
        moment; if it keeps happening, tell an operator.
      </p>
      <p className="text-sm text-destructive">{message}</p>
      {named !== undefined && (
        <p className="text-xs text-muted-foreground">
          Identity: {named}
          {identity.email !== undefined && identity.subject !== undefined
            ? ` (subject ${identity.subject})`
            : ''}
          {identity.issuer !== undefined ? ` from ${identity.issuer}` : ''}
        </p>
      )}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="justify-self-start"
        onClick={onSignOut}
      >
        <LogOut aria-hidden="true" />
        Sign out
      </Button>
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

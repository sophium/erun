import { Button } from 'erun-kit';
import type * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';

// The hero carries the pitch and both calls to action. Sign in stays the
// primary button so an existing user's path is exactly as short as the old
// bare card's — "Read the docs" is the secondary action a first-time visitor
// takes instead, and it renders only when platform discovery actually
// supplied a docs host (#1327: never a hardcoded fallback host).
export function LandingHero({
  tagline,
  description,
  docsUrl,
  oidc,
  fallbackReason,
}: {
  tagline: string;
  description: string;
  docsUrl: string | undefined;
  oidc: OidcConfig | undefined;
  fallbackReason: string | undefined;
}): React.ReactElement {
  const hasDocsUrl = docsUrl !== undefined && docsUrl.length > 0;
  return (
    <section className="border-b border-border bg-gradient-to-b from-muted/60 to-background px-6 py-16 sm:py-24">
      <div className="mx-auto flex max-w-3xl flex-col items-center gap-6 text-center">
        <h1 className="text-3xl font-bold tracking-tight text-foreground sm:text-5xl">{tagline}</h1>
        <p className="max-w-2xl text-base text-muted-foreground sm:text-lg">{description}</p>
        <div className="flex flex-col items-center gap-3 sm:flex-row">
          {oidc !== undefined ? (
            <Button
              type="button"
              size="lg"
              onClick={() => {
                void beginLogin(oidc);
              }}
            >
              Sign in
            </Button>
          ) : (
            <p className="max-w-sm text-xs text-muted-foreground">
              A bearer token is required to view your environments. Ask an operator for one, or
              configure OIDC sign-in for this instance.
            </p>
          )}
          {hasDocsUrl && (
            <Button type="button" variant="outline" size="lg" asChild>
              <a href={docsUrl} target="_blank" rel="noreferrer">
                Read the docs
              </a>
            </Button>
          )}
        </div>
        {fallbackReason !== undefined && (
          <p className="text-xs text-muted-foreground">{fallbackReason}</p>
        )}
      </div>
    </section>
  );
}

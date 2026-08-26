import { Button } from 'erun-kit';
import { Sparkles } from 'lucide-react';
import type * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';
import { LandingHeroVisual } from './LandingHeroVisual';

// The hero carries the pitch and both calls to action. Sign in stays the
// primary button so an existing user's path is exactly as short as the old
// bare card's — "Read the docs" is the secondary action a first-time visitor
// takes instead, and it renders only when platform discovery actually
// supplied a docs host: never a hardcoded fallback host. The
// two-column split at `lg` (copy left, product visual right) is what keeps
// the section from reading as a near-empty full-viewport banner, and what
// constrains the headline's measure so a long instance tagline still reads
// in a handful of lines instead of wrapping the width of the whole page.
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
    <section className="border-b border-border bg-gradient-to-b from-muted/40 to-background px-6 py-12 sm:py-16">
      <div className="mx-auto grid max-w-6xl gap-10 lg:grid-cols-2 lg:items-center lg:gap-16">
        <div className="flex flex-col items-center gap-6 text-center motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-4 motion-safe:duration-700 motion-safe:fill-mode-both lg:items-start lg:text-left">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-accent-brand/30 bg-accent-brand/10 px-3 py-1 text-xs font-medium text-accent-brand">
            <Sparkles aria-hidden="true" className="size-3.5" />
            Agentic development platform
          </span>
          <h1 className="max-w-2xl text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
            {tagline}
          </h1>
          <p className="max-w-lg text-base text-muted-foreground sm:text-lg">{description}</p>
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
        <LandingHeroVisual />
      </div>
    </section>
  );
}

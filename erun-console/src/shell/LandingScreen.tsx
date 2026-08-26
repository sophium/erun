import type * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { BrandMark } from './BrandMark';
import { DEFAULT_DESCRIPTION, DEFAULT_TAGLINE } from './landingContent';
import { LandingDifferentiators } from './LandingDifferentiators';
import { LandingFooter } from './LandingFooter';
import { LandingHero } from './LandingHero';

// LandingScreen is the signed-out route's front door (#1327), replacing the
// bare 384px SignInScreen card: a real <header>/<main>/<footer> landmark
// structure with an <h1> lead, so a first-time visitor learns what the
// product is and can reach the docs before ever being asked to sign in.
// `docsUrl`/`logoUrl` are per-instance platform config and render nothing
// when unset; `tagline` falls back to a bundled product-level default the
// same way BrandMark falls back to a generic mark.
export function LandingScreen({
  brand,
  logoUrl,
  tagline,
  docsUrl,
  oidc,
  fallbackReason,
}: {
  brand: string | undefined;
  logoUrl: string | undefined;
  tagline: string | undefined;
  docsUrl: string | undefined;
  oidc: OidcConfig | undefined;
  fallbackReason: string | undefined;
}): React.ReactElement {
  const brandLabel = brand !== undefined && brand.length > 0 ? brand : 'ERun';
  const heroTagline = tagline !== undefined && tagline.length > 0 ? tagline : DEFAULT_TAGLINE;
  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <header className="flex items-center gap-2.5 px-6 py-5">
        <BrandMark brand={brand} logoUrl={logoUrl} />
        <span className="text-sm font-semibold text-muted-foreground">{brandLabel}</span>
      </header>
      <main className="flex-1">
        <LandingHero
          tagline={heroTagline}
          description={DEFAULT_DESCRIPTION}
          docsUrl={docsUrl}
          oidc={oidc}
          fallbackReason={fallbackReason}
        />
        <LandingDifferentiators brandLabel={brandLabel} />
      </main>
      <LandingFooter brandLabel={brandLabel} docsUrl={docsUrl} />
    </div>
  );
}

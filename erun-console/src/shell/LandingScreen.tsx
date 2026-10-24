import type * as React from 'react';

import type { OidcConfig } from '../auth/auth';
import { BUNDLED_BRAND } from './brand';
import { BrandMark } from './BrandMark';
import { DEFAULT_DESCRIPTION, DEFAULT_TAGLINE } from './landingContent';
import { LandingDifferentiators } from './LandingDifferentiators';
import { LandingFooter } from './LandingFooter';
import { LandingHero } from './LandingHero';

// LandingScreen is the signed-out front door.
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
  const brandLabel = brand !== undefined && brand.length > 0 ? brand : BUNDLED_BRAND;
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

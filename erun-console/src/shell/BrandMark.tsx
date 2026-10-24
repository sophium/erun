import { cn } from 'erun-kit';
import * as React from 'react';

import { BUNDLED_BRAND } from './brand';

// A logo that fails to load falls back to the initial; the failure resets when a
// different URL resolves.
export function BrandMark({
  brand,
  logoUrl,
  className,
}: {
  brand: string | undefined;
  logoUrl?: string;
  className?: string;
}): React.ReactElement {
  const [logoFailed, setLogoFailed] = React.useState(false);
  React.useEffect(() => {
    setLogoFailed(false);
  }, [logoUrl]);
  const label = brand && brand.length > 0 ? brand : BUNDLED_BRAND;
  if (logoUrl !== undefined && logoUrl.length > 0 && !logoFailed) {
    return (
      <img
        src={logoUrl}
        alt=""
        onError={() => {
          setLogoFailed(true);
        }}
        className={cn('size-7 flex-none rounded-md object-contain', className)}
      />
    );
  }
  const initial = label.trim().charAt(0).toUpperCase();
  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex size-7 flex-none items-center justify-center rounded-md bg-primary text-sm font-semibold text-primary-foreground',
        className,
      )}
    >
      {initial}
    </span>
  );
}

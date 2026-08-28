import { cn } from 'erun-kit';
import * as React from 'react';

// BrandMark renders an instance's logo when platform discovery (GET
// /v1/platform) carries one (`logoUrl`), and falls back to the product
// initial otherwise — the fallback must never assume a hardcoded instance
// name.
//
// A configured URL that fails to load falls back to the same mark: an
// operator's logo lives on their own host, so a moved asset, a blocked
// origin, or a typo is a real state, and a broken image icon on the front
// door is a dead end nothing on the page explains. The failure is reset when
// a different logo resolves, so one bad URL does not suppress the next.
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
  const label = brand && brand.length > 0 ? brand : 'ERun';
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

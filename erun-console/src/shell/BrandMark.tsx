import { cn } from 'erun-kit';
import type * as React from 'react';

// BrandMark renders the platform's initial as a mark, since the platform
// contract (GET /v1/platform) carries a display name (`brand`) but no logo
// asset — there is nothing image-shaped to render yet. It falls back to the
// generic product initial when no instance brand is configured, never a
// hardcoded instance name.
export function BrandMark({
  brand,
  className,
}: {
  brand: string | undefined;
  className?: string;
}): React.ReactElement {
  const label = brand && brand.length > 0 ? brand : 'ERun';
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

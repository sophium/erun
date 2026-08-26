import { cn } from 'erun-kit';
import type * as React from 'react';

// BrandMark renders an instance's logo when platform discovery (GET
// /v1/platform) carries one (`logoUrl`), and falls back to the product
// initial otherwise — no instance sets a logo yet, so this fallback is the
// only path exercised today, and it must never assume a hardcoded instance
// name.
export function BrandMark({
  brand,
  logoUrl,
  className,
}: {
  brand: string | undefined;
  logoUrl?: string;
  className?: string;
}): React.ReactElement {
  const label = brand && brand.length > 0 ? brand : 'ERun';
  if (logoUrl !== undefined && logoUrl.length > 0) {
    return (
      <img
        src={logoUrl}
        alt=""
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

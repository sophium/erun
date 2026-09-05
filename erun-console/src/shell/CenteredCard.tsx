import { Card, CardContent, CardHeader, CardTitle } from 'erun-kit';
import type * as React from 'react';

import { BrandMark } from './BrandMark';

// CenteredCard is the shared frame for every pre-shell screen (sign-in, not
// enrolled, error, loading): a branded card centered in the viewport, so the
// first thing an Operator of any hosted instance sees carries that instance's
// own identity instead of an unstyled paragraph.
export function CenteredCard({
  brand,
  title,
  role,
  children,
}: {
  brand: string | undefined;
  title: string;
  role: 'status' | 'alert';
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <div className="flex h-dvh min-h-0 w-full items-center justify-center bg-background px-4 text-foreground">
      <Card className="w-full max-w-sm" role={role}>
        <CardHeader>
          <div className="flex items-center gap-2.5">
            <BrandMark brand={brand} />
            <span className="truncate text-sm font-semibold text-muted-foreground">
              {brand && brand.length > 0 ? brand : 'ERun console'}
            </span>
          </div>
          <CardTitle className="text-lg">{title}</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-3">{children}</CardContent>
      </Card>
    </div>
  );
}

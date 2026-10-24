import * as React from 'react';

import { Label } from './ui/label';

// FieldLabel marks required fields with a glyph plus a visually hidden "(required)" for
// the accessible name (WCAG 1.4.1).
export function FieldLabel({
  htmlFor,
  required,
  children,
}: {
  htmlFor: string;
  required?: boolean;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <Label htmlFor={htmlFor}>
      {children}
      {required && (
        <>
          <span aria-hidden="true" className="ml-0.5 text-destructive">
            *
          </span>
          <span className="sr-only"> (required)</span>
        </>
      )}
    </Label>
  );
}

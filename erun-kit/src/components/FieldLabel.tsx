import * as React from 'react';

import { Label } from './ui/label';

// FieldLabel is the shared label for form fields in the app's dialogs. It renders
// an explicit required marker so users can see at a glance which fields are
// mandatory (recognition over recall) instead of discovering it only when a
// disabled submit button names one missing value at a time. The asterisk is a
// glyph, not a colour-only cue, and the visually-hidden "(required)" folds into
// the control's accessible name via htmlFor, so the requirement is conveyed
// non-visually too (WCAG 1.4.1).
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

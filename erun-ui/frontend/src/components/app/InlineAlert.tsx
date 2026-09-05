import { AlertCircle, Lock } from 'lucide-react';
import * as React from 'react';

// InlineAlert is the shared surface for a write that was attempted and
// refused, beside the control that attempted it. It carries the same
// destructive banner the dialogs already use, plus an icon so the failure is
// not signalled by colour alone (WCAG 1.4.1) and an alert role so it is
// announced when it appears (WCAG 4.1.3). Long values wrap instead of
// widening the row the alert sits in.
export function InlineAlert({ children }: { children: React.ReactNode }): React.ReactElement {
  return (
    <div
      role="alert"
      className="flex w-full items-start gap-2 rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]"
    >
      <AlertCircle className="mt-px size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0">{children}</span>
    </div>
  );
}

// PermissionNotice is the shared "you may not see/do this" surface: a caller
// missing access is a state, not a fault, so it gets a neutral treatment and
// role="status" rather than InlineAlert's destructive role="alert" — the same
// distinction ReviewPanel.ts's reachabilityStatuses draws for "not a fault".
// Replaces a permission note dropped in a layout gap as plain muted text,
// which reads as inert body copy beside the control it explains (#1378).
export function PermissionNotice({ children }: { children: React.ReactNode }): React.ReactElement {
  return (
    <div
      role="status"
      className="flex w-full items-start gap-2 rounded-[var(--radius)] border border-border bg-muted/40 px-[11px] py-[9px] text-[13px] leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
    >
      <Lock className="mt-px size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0">{children}</span>
    </div>
  );
}

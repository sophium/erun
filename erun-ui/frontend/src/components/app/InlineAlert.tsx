import { AlertCircle, Lock } from 'lucide-react';
import * as React from 'react';

// InlineAlert is the shared surface for a refused write: icon plus alert role
// (WCAG 1.4.1, 4.1.3). `action` renders on its own row for failures a retry fixes.
export function InlineAlert({
  children,
  action,
  id,
}: {
  children: React.ReactNode;
  action?: React.ReactNode;
  // Stable handle when several alerts are on screen at once.
  id?: string;
}): React.ReactElement {
  const message = (
    <div
      id={id}
      role="alert"
      className="flex w-full items-start gap-2 rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]"
    >
      <AlertCircle className="mt-px size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0">{children}</span>
    </div>
  );
  if (!action) {
    return message;
  }
  return (
    <div className="grid gap-2">
      {message}
      <div className="flex justify-end gap-1.5">{action}</div>
    </div>
  );
}

// PermissionNotice treats missing access as a state, not a fault: role="status".
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

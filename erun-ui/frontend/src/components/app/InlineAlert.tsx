import { AlertCircle, Lock } from 'lucide-react';
import * as React from 'react';

// InlineAlert is the shared surface for a write that was attempted and
// refused, beside the control that attempted it. It carries the same
// destructive banner the dialogs already use, plus an icon so the failure is
// not signalled by colour alone (WCAG 1.4.1) and an alert role so it is
// announced when it appears (WCAG 4.1.3). Long values wrap instead of
// widening the row the alert sits in.
// `action` is opt-in and stays opt-in: where the only remedy is a person
// ("ask an administrator") the alert is still the single row it always was.
// It exists for the other case -- a failure a re-ask fixes, where naming the
// cause without offering the remedy is a dead end. The control renders
// on its own row beneath the message rather than being crushed into it, so a
// long message wraps without dragging the button along. The Jobs list is the
// read path that needed this: it told the operator the read had timed out and
// left them to switch tabs and back, while the unreachable-runtime card a tab
// away already carried a Retry beside its own message.
export function InlineAlert({
  children,
  action,
  id,
}: {
  children: React.ReactNode;
  action?: React.ReactNode;
  // Optional stable handle for the alert a caller needs to address
  // unambiguously. Several of these can be on screen at once -- the sidebar
  // and the panel it opened each carry their own -- so a page-wide
  // getByRole('alert') matches more than one the moment anything else on the
  // screen is in a failed state.
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

// PermissionNotice is the shared "you may not see/do this" surface: a caller
// missing access is a state, not a fault, so it gets a neutral treatment and
// role="status" rather than InlineAlert's destructive role="alert" — the same
// distinction ReviewPanel.ts's reachabilityStatuses draws for "not a fault".
// Replaces a permission note dropped in a layout gap as plain muted text,
// which reads as inert body copy beside the control it explains.
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

import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { cn } from '@/lib/utils';

// BusyRowSpinner is the shared "this row is working" indicator for sidebar
// rows. Shared by AI-orchestrator and environment rows so both report activity
// identically — an orchestrator driving its envs is working in exactly the
// sense an env's AI tab is, and a row that looks idle while it works is the
// worse failure. Independent of the status dot, which carries the row's
// condition rather than its activity. The caller owns the accessible label.
//
// Forwards ref and every other prop to the underlying icon, not just
// `label`: a caller wrapping this in IconTooltip's `asChild` Slot merges in
// the pointer/focus handlers and ref that make the trigger actually work, and
// a component that drops them renders a tooltip that never opens on hover —
// silently, since the icon still looks and spins the same either way.
export const BusyRowSpinner = React.forwardRef<
  SVGSVGElement,
  React.SVGProps<SVGSVGElement> & { label: string }
>(function BusyRowSpinner({ label, className, ...props }, ref) {
  return (
    <LoaderCircle
      ref={ref}
      className={cn('size-3.5 flex-none animate-spin text-current opacity-75', className)}
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
      {...props}
    />
  );
});

import { cn } from 'erun-kit';
import { Hand } from 'lucide-react';
import * as React from 'react';

// AwaitingInputIndicator is the sidebar's "the AI tool is waiting on you"
// marker: the AI tab reported a turn boundary, so the work is not in flight and
// the row must not spin — but it is not idle either, and rendering it as the
// absence of a spinner is exactly the defect this exists to fix. A session
// blocked on the operator prints nothing, which is what a finished one looks
// like from the stream, so no volume heuristic can draw this state.
//
// Deliberately not a variant of BusyRowSpinner: the shape and the absence of
// animation are what separate the two, and a second colour would compete with
// the status dot, which already owns the row's condition. The tone is inherited
// so the marker reads the same on a selected row as on any other. The caller
// owns the accessible label, as BusyRowSpinner's does.
//
// Forwards ref and every other prop to the underlying icon for the same reason
// BusyRowSpinner does: a caller wrapping this in IconTooltip's `asChild` Slot
// merges in the pointer/focus handlers and ref the tooltip needs, and a
// component that drops them renders a tooltip that silently never opens.
export const AwaitingInputIndicator = React.forwardRef<
  SVGSVGElement,
  React.SVGProps<SVGSVGElement> & { label: string }
>(function AwaitingInputIndicator({ label, className, ...props }, ref) {
  return (
    <Hand
      ref={ref}
      className={cn('size-3.5 flex-none text-current opacity-75', className)}
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
      {...props}
    />
  );
});

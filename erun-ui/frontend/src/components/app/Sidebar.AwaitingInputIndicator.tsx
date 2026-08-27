import { cn } from 'erun-kit';
import { MessageCircleQuestion } from 'lucide-react';
import * as React from 'react';

// AwaitingInputIndicator is the sidebar row's "the Agent is waiting on you"
// glyph — the state a PTY-output-volume heuristic could never produce, since a
// session waiting on a human and one that finished are both silent. It is
// visually distinct from BusyRowSpinner (a still glyph, not a spinner; a
// warning tone, not the current-color spinner) so an operator scanning the
// sidebar cannot mistake "still working" for "needs you" — the two states
// this whole model exists to tell apart.
export const AwaitingInputIndicator = React.forwardRef<
  SVGSVGElement,
  React.SVGProps<SVGSVGElement> & { label: string }
>(function AwaitingInputIndicator({ label, className, ...props }, ref) {
  return (
    <MessageCircleQuestion
      ref={ref}
      className={cn('size-3.5 flex-none text-amber-500', className)}
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
      {...props}
    />
  );
});

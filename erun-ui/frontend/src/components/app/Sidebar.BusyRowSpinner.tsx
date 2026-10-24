import { cn } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

// BusyRowSpinner is the shared row-activity indicator; the caller owns the label.
// Forwards ref and all props so an IconTooltip asChild trigger works.
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

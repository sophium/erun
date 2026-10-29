import * as React from 'react';

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip';

// One provider per tooltip: Radix's per-provider pointer-in-transit latch otherwise
// refuses the handover between neighbouring triggers. This keeps hoverable content,
// which disableHoverableContent would drop (WCAG 2.2 SC 1.4.13).
export function IconTooltip({
  label,
  children,
}: {
  label: string;
  children: React.ReactElement;
}): React.ReactElement {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>{children}</TooltipTrigger>
        <TooltipContent>{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

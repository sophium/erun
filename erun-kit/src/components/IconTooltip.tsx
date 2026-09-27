import * as React from 'react';

import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';

// The content is a bare text label and never interactive, so there is nothing
// to hover into and reaching for the content is not a gesture this tooltip
// serves. Leaving Radix's hoverable-content grace area on costs the sibling
// handover instead: leaving one trigger arms a provider-wide
// pointer-in-transit latch, and a trigger's own pointermove is dispatched
// before the document-level tracker that clears it, so arriving at a
// neighbouring trigger in a single pointermove raises no tooltip at all.
export function IconTooltip({
  label,
  children,
}: {
  label: string;
  children: React.ReactElement;
}): React.ReactElement {
  return (
    <Tooltip disableHoverableContent>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

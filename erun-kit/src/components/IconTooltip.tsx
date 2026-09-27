import * as React from 'react';

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip';

// A tooltip per provider, so each one carries its own pointer-in-transit latch.
//
// Radix gives a provider a single `isPointerInTransitRef`, armed when a trigger
// is left while its content stays hoverable, and every trigger under that
// provider refuses to open while it is set. The latch is cleared by a
// document-level `pointermove` tracker that TooltipContentHoverable installs in
// an effect when it builds its grace area -- and that effect has not run yet
// when the arrival `pointermove` fires, because the browser dispatches the
// out/leave/over/enter/move of one physical movement in a single task. So a
// handover between neighbouring triggers is refused, and stays refused while
// the pointer rests inside the previous tooltip's grace area, which is where a
// reach for the adjacent shortcut lands. The tooltip does not mount late; it
// does not mount at all.
//
// One provider per tooltip gives the arriving trigger a latch nothing else can
// set, so it opens on the move that reaches it. The departing tooltip is
// dismissed by Radix's own coordination rather than by anything here: opening
// any tooltip dispatches `tooltip.open` on the document, and every mounted
// tooltip content closes itself on that event (TooltipContentImpl). Both labels
// are never up together -- the departing one animates out and leaves.
//
// Scoping the latch is what lets this keep Radix's hoverable content. Setting
// `disableHoverableContent` instead would buy the same handover by releasing
// the content as the pointer leaves the trigger, which costs WCAG 2.2 SC 1.4.13
// "Hoverable": the label could then only be read from a distance and never from
// under the pointer. The content here is a bare text label and never
// interactive, so nothing about the tooltip's own behaviour needs that, but the
// success criterion is about the operator's pointer, not about whether a click
// is on offer.
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

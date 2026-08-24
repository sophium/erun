import { Tooltip, TooltipContent, TooltipTrigger } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

export function TerminalBusyOverlay({ message }: { message: string }): React.ReactElement | null {
  if (!message) {
    return null;
  }

  return (
    <div
      className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-terminal/45"
      role="status"
      aria-live="polite"
    >
      <div className="pointer-events-auto flex max-w-[min(520px,calc(100%-48px))] items-center gap-3 rounded-md border border-[oklch(0.28_0_0)] bg-[oklch(0.08_0_0)] px-4 py-3 text-[13px] leading-[1.35] text-[oklch(0.86_0_0)] shadow-lg">
        <LoaderCircle
          className="size-4 flex-none animate-spin text-[oklch(0.72_0_0)]"
          aria-hidden="true"
        />
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="min-w-0 cursor-default truncate border-0 bg-transparent p-0 text-left text-inherit outline-none focus-visible:ring-1 focus-visible:ring-ring/40"
              aria-label={message}
            >
              {message}
            </button>
          </TooltipTrigger>
          <TooltipContent
            side="top"
            className="max-w-[480px] whitespace-normal text-left leading-5"
          >
            {message}
          </TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}

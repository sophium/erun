import { CheckCircle2 } from 'lucide-react';
import type * as React from 'react';

// The hero's product visual: a live console signed-out into no
// tenant has nothing real to screenshot, so this is a considered abstract
// stand-in — an editor pane and an agent pane sharing one window, the same
// "side by side" claim the differentiators section states in words. Built
// from the console's own tokens (not a static image) so it repaints for
// dark mode for free, the same reason LandingDifferentiators avoided the
// docs site's hand-drawn diagram.
export function LandingHeroVisual(): React.ReactElement {
  return (
    <div
      aria-hidden="true"
      className="relative mx-auto w-full max-w-md motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-6 motion-safe:duration-700 motion-safe:delay-150 motion-safe:fill-mode-both lg:mx-0"
    >
      <div className="absolute -inset-8 -z-10 rounded-[2rem] bg-accent-brand/10 blur-3xl" />
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-lg">
        <div className="flex items-center gap-1.5 border-b border-border px-4 py-3">
          <span className="size-2.5 rounded-full bg-destructive/60" />
          <span className="size-2.5 rounded-full bg-accent-brand/50" />
          <span className="size-2.5 rounded-full bg-muted-foreground/30" />
          <span className="ml-2 truncate text-xs text-muted-foreground">
            feature/checkout-refactor
          </span>
        </div>
        <div className="grid grid-cols-2 divide-x divide-border">
          <div className="flex flex-col gap-2 p-4">
            <div className="h-2 w-3/4 rounded-full bg-foreground/15" />
            <div className="h-2 w-1/2 rounded-full bg-accent-brand/40" />
            <div className="h-2 w-5/6 rounded-full bg-foreground/10" />
            <div className="h-2 w-2/3 rounded-full bg-foreground/10" />
            <div className="h-2 w-1/3 rounded-full bg-accent-brand/40" />
          </div>
          <div className="flex flex-col gap-2 p-4">
            <div className="flex items-center gap-1.5 text-xs font-medium text-accent-brand">
              <span className="relative flex size-1.5">
                <span className="absolute inline-flex size-full animate-ping rounded-full bg-accent-brand opacity-75 motion-reduce:hidden" />
                <span className="relative inline-flex size-1.5 rounded-full bg-accent-brand" />
              </span>
              Agent
            </div>
            <div className="rounded-md bg-muted px-2 py-1.5 text-[10px] leading-snug text-muted-foreground">
              Running tests…
            </div>
            <div className="rounded-md bg-muted px-2 py-1.5 text-[10px] leading-snug text-muted-foreground">
              3 files changed
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2 border-t border-border bg-muted/40 px-4 py-2.5 text-xs text-muted-foreground">
          <CheckCircle2 className="size-3.5 text-accent-brand" />
          All checks passed
        </div>
      </div>
    </div>
  );
}

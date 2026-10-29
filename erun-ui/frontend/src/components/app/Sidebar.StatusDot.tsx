import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

export type StatusDotState = 'running' | 'busy' | 'stopped' | 'failed';

// StatusDotGlyph carries state by shape, not colour alone (WCAG 1.4.1); the
// caller owns the accessible label and any interaction.
export function StatusDotGlyph({ state }: { state: StatusDotState }): React.ReactElement {
  if (state === 'failed') {
    return <TriangleAlert aria-hidden="true" className="size-2.5 text-amber-500" />;
  }
  if (state === 'busy') {
    return (
      <span
        aria-hidden="true"
        className="flex size-3 items-center justify-center rounded-full border-[1.5px] border-emerald-500 motion-safe:animate-pulse"
      >
        <span className="block size-1.5 rounded-full bg-emerald-500" />
      </span>
    );
  }
  if (state === 'stopped') {
    return (
      <span
        aria-hidden="true"
        className="block size-2 rounded-full border-[1.5px] border-muted-foreground bg-transparent"
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      className="block size-2 rounded-full bg-emerald-500 shadow-[0_0_0_1px_color-mix(in_oklch,currentColor_20%,transparent)]"
    />
  );
}

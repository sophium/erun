import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

// The status a sidebar row's dot conveys. Shared by AI-orchestrator and
// environment rows so both render an identical indicator.
export type StatusDotState = 'running' | 'stopped' | 'failed';

// StatusDotGlyph is the shared status indicator for sidebar rows. State is
// carried by SHAPE, not colour alone (WCAG non-colour-only status): a filled
// emerald dot = running, a hollow ring = stopped, a warning triangle = failed.
// It is fixed-size so every row's dot matches; the caller owns the accessible
// label and any interaction (a passive status light or a click target).
export function StatusDotGlyph({ state }: { state: StatusDotState }): React.ReactElement {
  if (state === 'failed') {
    return <TriangleAlert aria-hidden="true" className="size-2.5 text-amber-500" />;
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

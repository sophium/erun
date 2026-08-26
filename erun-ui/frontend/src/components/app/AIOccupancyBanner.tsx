import { Button } from 'erun-kit';
import { UserRound } from 'lucide-react';
import * as React from 'react';

import type { UIEnvironmentLease, UISelection } from '@/types';

interface AIOccupancyBannerProps {
  leases: UIEnvironmentLease[];
  selection: UISelection | null;
  onShowJobs?: () => void;
}

// AIOccupancyBanner is the "while in the tab" half of erun#1221: a persistent,
// non-blocking reminder — not a one-time toast — that another job is still
// working in this environment while the operator's own AI tab is open.
// Visually mirrors ActivityLockOverlay so a comparable "something else is
// happening here" condition reads the same way across the terminal pane.
export function AIOccupancyBanner({
  leases,
  selection,
  onShowJobs,
}: AIOccupancyBannerProps): React.ReactElement {
  const names = leases.map((lease) => lease.name).join(', ');
  // Only a lease a job holds can be acted on; anything else has no job behind
  // it to stop, so the banner names the occupancy and offers nothing it cannot
  // deliver.
  const cancellable = leases.filter((lease) => !!lease.jobId);
  return (
    <div
      className="pointer-events-none absolute top-3 right-3 z-10 flex max-w-[min(360px,calc(100%_-_1.5rem))] flex-col items-end gap-1"
      role="status"
      aria-live="polite"
    >
      <div className="pointer-events-auto flex w-full flex-col gap-1 rounded-md border bg-card/95 p-3 text-xs shadow-lg backdrop-blur-[2px]">
        <div className="flex items-center gap-2 text-sm font-medium">
          <UserRound aria-hidden="true" className="size-4 text-amber-500" />
          <span>Another agent is working here</span>
        </div>
        <p className="text-[11px] leading-5 text-muted-foreground">
          <span className="font-medium text-foreground">{names}</span> is also running in this
          environment and competing for the same pod resources.
        </p>
        {selection && cancellable.length > 0 && onShowJobs && (
          <div className="flex justify-end">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onShowJobs}
              aria-label={`Show the jobs running in ${selection.environment}`}
            >
              View jobs
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

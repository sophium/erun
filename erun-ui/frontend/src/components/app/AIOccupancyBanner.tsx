import { Button } from 'erun-kit';
import { UserRound } from 'lucide-react';
import * as React from 'react';

import type { UIEnvironmentLease, UISelection } from '@/types';

interface AIOccupancyBannerProps {
  leases: UIEnvironmentLease[];
  selection: UISelection | null;
  onShowJobs?: () => void;
}

// AIOccupancyBanner is a persistent reminder that another job is working in
// this environment while its AI tab is open.
export function AIOccupancyBanner({
  leases,
  selection,
  onShowJobs,
}: AIOccupancyBannerProps): React.ReactElement {
  const names = leases.map((lease) => lease.name).join(', ');
  // Only a job-held lease can be stopped.
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

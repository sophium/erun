import * as React from 'react';
import { LoaderCircle } from 'lucide-react';

import type { ActivityLockEvent } from '@/app/activityQueueState';
import { Button } from '@/components/ui/button';

type ActivityLockOverlayProps = {
  lock: ActivityLockEvent;
  onOpenQueue: () => void;
  onProceedAnyway?: () => void;
};

// ActivityLockOverlay renders a small badge over a terminal whose session
// is currently waiting on an activity (deploy/build/release) that touches
// the runtime hosting it. The overlay is intentionally non-blocking:
// pointer events pass through everywhere except the inline card itself,
// so the underlying terminal stays interactive — the in-pod CLI may
// prompt for input (helm pending-release recovery, etc.) and the user
// must be able to answer it without first dismissing the overlay.
//
// "Open anyway" hides the overlay locally for this session without
// affecting the activity record on the backend.
export function ActivityLockOverlay({ lock, onOpenQueue, onProceedAnyway }: ActivityLockOverlayProps): React.ReactElement {
  return (
    <div
      className="pointer-events-none absolute top-3 right-3 z-10 flex max-w-[360px] flex-col items-end gap-1"
      role="status"
      aria-live="polite"
    >
      <div className="pointer-events-auto flex w-full flex-col gap-2 rounded-md border bg-card/95 p-3 text-xs shadow-lg backdrop-blur-[2px]">
        <div className="flex items-center gap-2 text-sm font-medium">
          <LoaderCircle aria-hidden="true" className="size-4 animate-spin text-blue-500" />
          <span>{lock.reason || 'Waiting for activity to complete'}</span>
        </div>
        <p className="text-[11px] leading-5 text-muted-foreground">
          {lock.deployTarget ? <><span className="font-medium text-foreground">{lock.deployTarget}</span> is in flight. </> : null}
          The terminal stays interactive — answer any CLI prompts as usual; this notice will hide automatically when the activity finishes.
        </p>
        <div className="flex justify-end gap-2 pt-1">
          {onProceedAnyway && (
            <Button type="button" variant="ghost" size="xs" className="text-[11px]" onClick={onProceedAnyway}>
              Hide
            </Button>
          )}
          <Button type="button" variant="default" size="xs" className="text-[11px]" onClick={onOpenQueue}>
            View activity
          </Button>
        </div>
      </div>
    </div>
  );
}

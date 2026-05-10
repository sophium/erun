import * as React from 'react';
import { LoaderCircle } from 'lucide-react';

import type { ActivityLockEvent } from '@/app/activityQueueState';
import { Button } from '@/components/ui/button';

type ActivityLockOverlayProps = {
  lock: ActivityLockEvent;
  onOpenQueue: () => void;
  onProceedAnyway?: () => void;
};

// ActivityLockOverlay renders a translucent card over a terminal whose
// session is currently locked because an activity (deploy/build/release/
// open) is in flight against the runtime that hosts it. The card is
// non-blocking: it covers the terminal area but the user can still open
// the activity queue or, with explicit override, dismiss the lock locally
// if they need emergency access.
export function ActivityLockOverlay({ lock, onOpenQueue, onProceedAnyway }: ActivityLockOverlayProps): React.ReactElement {
  return (
    <div
      className="absolute inset-0 z-10 flex items-center justify-center bg-background/85 backdrop-blur-[2px]"
      role="status"
      aria-live="polite"
    >
      <div className="flex w-[360px] flex-col gap-2 rounded-md border bg-card p-4 shadow-lg">
        <div className="flex items-center gap-2 text-sm font-medium">
          <LoaderCircle aria-hidden="true" className="size-4 animate-spin text-blue-500" />
          <span>{lock.reason || 'Waiting for activity to complete'}</span>
        </div>
        <p className="text-xs leading-5 text-muted-foreground">
          {lock.deployTarget ? <><span className="font-medium text-foreground">{lock.deployTarget}</span> is in flight. </> : null}
          This terminal will reattach automatically when the activity finishes.
        </p>
        <div className="flex justify-end gap-2 pt-1">
          {onProceedAnyway && (
            <Button type="button" variant="ghost" size="sm" className="text-xs" onClick={onProceedAnyway}>
              Open anyway
            </Button>
          )}
          <Button type="button" variant="default" size="sm" className="text-xs" onClick={onOpenQueue}>
            View activity
          </Button>
        </div>
      </div>
    </div>
  );
}

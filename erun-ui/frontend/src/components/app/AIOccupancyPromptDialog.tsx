import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { LoaderCircle, UserRound } from 'lucide-react';
import * as React from 'react';

import { cancelAIOccupancyPrompt, confirmAIOccupancyPrompt } from '@/app/aiOccupancyThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';

// heldSince renders a lease's age in the coarsest unit that stays readable —
// "started just now" reads better than "started 0m ago" for a fresh lease.
function heldSince(secondsHeld: number | undefined): string {
  if (!secondsHeld || secondsHeld < 60) {
    return 'started just now';
  }
  const minutes = Math.floor(secondsHeld / 60);
  if (minutes < 60) {
    return `running for ${String(minutes)}m`;
  }
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `running for ${String(hours)}h${remainingMinutes ? String(remainingMinutes) + 'm' : ''}`;
}

// AIOccupancyPromptDialog is erun#1221's "confirm, don't silently start a
// second agent": opening the AI tab found the environment already held by
// another job's activity lease. Starting anyway is a deliberate choice, not a
// blocked one — there are legitimate reasons to run two (a long build in one,
// a quick question in the other) — so this names the occupant and lets the
// operator choose rather than refusing outright.
export function AIOccupancyPromptDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const prompt = useAppSelector((state) => state.aiOccupancyPrompt);
  const open = prompt.open;
  const starting = prompt.starting;
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !starting) {
          dispatch(cancelAIOccupancyPrompt());
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Another agent is already working here</DialogTitle>
          <DialogDescription>
            Starting the AI tab will run a second agent alongside it in the same environment — both
            will compete for the same pod&apos;s CPU, memory and disk.
          </DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col gap-1.5 rounded-[var(--radius)] border bg-muted/40 px-3 py-2.5 text-[13px]">
          {prompt.leases.map((lease) => (
            <li key={lease.name} className="flex items-center gap-2">
              <UserRound className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span className="font-medium text-foreground">{lease.name}</span>
              <span className="text-muted-foreground">{heldSince(lease.secondsHeld)}</span>
            </li>
          ))}
        </ul>
        {prompt.error && (
          <div
            role="alert"
            className="rounded-[var(--radius)] border border-destructive/40 bg-destructive/10 px-3 py-2.5 text-[13px] leading-[1.4] text-destructive"
          >
            {prompt.error}
          </div>
        )}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={starting}
            onClick={() => {
              dispatch(cancelAIOccupancyPrompt());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={starting}
            onClick={() => {
              void dispatch(confirmAIOccupancyPrompt());
            }}
          >
            {starting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Start anyway
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

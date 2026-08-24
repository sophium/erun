import { AlertTriangle, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { cancelCloseGate, confirmCloseGate } from '@/app/windowCloseThunks';
import { activityTargetLabel } from '@/components/app/ActivityQueueDrawer.helpers';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

// Shown when the operator tries to close the window while a build, deploy, or
// release is still running. Closing used to SIGKILL every one of these with
// no warning and no record (erun#1214); this names what is running before
// the operator chooses, per erun-ui/AGENTS.md's "clear action boundaries"
// rule for destructive/side-effect flows (Nielsen #1 visibility of system
// status, #2 recognition over recall — the operator sees exactly what would
// be lost instead of having to remember what they started).
export function CloseConfirmDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const gate = useAppSelector((state) => state.closeGate);

  return (
    <Dialog
      open={gate.open}
      onOpenChange={(next) => {
        if (!next && !gate.confirming) {
          dispatch(cancelCloseGate());
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Close ERun while work is still running?</DialogTitle>
          <DialogDescription>
            Closing the window stops every job below immediately. Anything not finished is lost, not
            paused — a release could leave a build published without its git tag, or a deploy
            half-applied.
          </DialogDescription>
        </DialogHeader>
        <ul className="grid gap-1.5 rounded-[var(--radius)] border border-border bg-muted/30 px-3 py-2.5 text-[13px]">
          {gate.running.map((entry) => (
            <li key={entry.id} className="flex items-center justify-between gap-2">
              <span className="font-medium uppercase tracking-wide text-[11px] text-muted-foreground">
                {entry.command}
              </span>
              <span className="truncate text-foreground">{activityTargetLabel(entry)}</span>
            </li>
          ))}
        </ul>
        {gate.error && (
          <div
            role="alert"
            className="rounded-[var(--radius)] border border-destructive/40 bg-destructive/10 px-3 py-2.5 text-[13px] leading-[1.4] text-destructive"
          >
            <AlertTriangle className="mr-1.5 inline size-3.5" aria-hidden="true" />
            Could not record the interrupted work: {gate.error}. Closing anyway.
          </div>
        )}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={gate.confirming}
            onClick={() => {
              dispatch(cancelCloseGate());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={gate.confirming}
            onClick={() => {
              void dispatch(confirmCloseGate());
            }}
          >
            {gate.confirming && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Close anyway
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

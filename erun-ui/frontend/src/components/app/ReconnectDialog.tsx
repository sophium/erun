import { AlertCircle, LoaderCircle, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reconnectCopy } from '@/app/reconnectCopy';
import { cancelReconnect, confirmReconnect } from '@/app/reviewThunks';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

// ReconnectDialog reads only the review.reconnect slice; subscribing through
// useAppSelector keeps re-renders scoped to that slice instead of the full
// AppState shape.
export function ReconnectDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const reconnect = useAppSelector((state) => state.review.reconnect);
  const open = reconnect.status !== 'idle';
  const running = reconnect.status === 'running';
  const failed = reconnect.status === 'error';
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(cancelReconnect());
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{reconnectCopy.dialogTitle}</DialogTitle>
          <DialogDescription>{reconnectCopy.dialogBody}</DialogDescription>
        </DialogHeader>
        {(running || failed) && (
          <div className="rounded-[var(--radius)] border bg-muted/40 px-3 py-2.5 text-[13px] leading-[1.4]">
            {running && (
              <div className="flex items-start gap-2 text-muted-foreground">
                <LoaderCircle
                  className="mt-px size-[14px] flex-none animate-spin"
                  aria-hidden="true"
                />
                <div className="min-w-0 [overflow-wrap:anywhere]">
                  <div className="font-medium text-foreground">{reconnectCopy.runningStatus}</div>
                  <div className="mt-0.5 truncate font-mono text-[12px] text-muted-foreground">
                    {reconnect.lastLine || reconnectCopy.runningHint}
                  </div>
                </div>
              </div>
            )}
            {failed && (
              <div className="flex items-start gap-2">
                <AlertCircle
                  className="mt-px size-[14px] flex-none text-destructive"
                  aria-hidden="true"
                />
                <div className="min-w-0 [overflow-wrap:anywhere]">
                  <div className="font-medium text-destructive">
                    {reconnectCopy.errorStatusTitle}
                  </div>
                  <div className="mt-0.5 text-muted-foreground">{reconnect.error}</div>
                </div>
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={running}
            onClick={() => {
              dispatch(cancelReconnect());
            }}
          >
            {reconnectCopy.dialogCancel}
          </Button>
          <Button
            type="button"
            disabled={running}
            onClick={() => {
              void dispatch(confirmReconnect());
            }}
          >
            {running ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <RefreshCw aria-hidden="true" />
            )}
            {reconnectCopy.dialogConfirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

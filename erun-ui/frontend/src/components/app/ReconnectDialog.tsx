import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { Play, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reachabilityCopy, reconnectCopy } from '@/app/reconnectCopy';
import { cancelReconnect, confirmReconnect } from '@/app/reviewThunks';

// Confirmation only; in-flight and failure states live in the non-modal
// ReconnectStatusPanel so other environments stay interactive while one reconnects.
export function ReconnectDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const status = useAppSelector((state) => state.review.reconnect.status);
  const kind = useAppSelector((state) => state.review.reconnect.kind);
  const copy = reachabilityCopy[kind];
  const open = status === 'confirm';
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
          <DialogTitle>{copy.dialogTitle}</DialogTitle>
          <DialogDescription>{copy.dialogBody}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              dispatch(cancelReconnect());
            }}
          >
            {reconnectCopy.dialogCancel}
          </Button>
          <Button
            type="button"
            onClick={() => {
              void dispatch(confirmReconnect());
            }}
          >
            {kind === 'not-open' ? <Play aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
            {copy.dialogConfirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

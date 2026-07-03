import { RefreshCw } from 'lucide-react';
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

// Confirmation only; in-flight and failure states live in the non-modal
// ReconnectStatusPanel so other environments stay interactive while one reconnects.
export function ReconnectDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const status = useAppSelector((state) => state.review.reconnect.status);
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
          <DialogTitle>{reconnectCopy.dialogTitle}</DialogTitle>
          <DialogDescription>{reconnectCopy.dialogBody}</DialogDescription>
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
            <RefreshCw aria-hidden="true" />
            {reconnectCopy.dialogConfirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

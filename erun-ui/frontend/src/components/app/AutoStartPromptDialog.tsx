import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { LoaderCircle, Play, ShieldOff } from 'lucide-react';
import * as React from 'react';

import { cancelAutoStartPrompt, confirmAutoStartPrompt } from '@/app/autoStartThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';

// Asks once per remote environment whether opening it should auto-start its
// stopped EC2 instance, and persists the answer so the prompt does not
// reappear until the operator resets it.
export function AutoStartPromptDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const prompt = useAppSelector((state) => state.autoStartPrompt);
  const tenantName = prompt.selection?.tenant ?? '';
  const environmentName = prompt.selection?.environment ?? '';
  const saving = prompt.saving;
  const open = prompt.open;
  // Tracks which button was clicked so the spinner renders only on that
  // one; otherwise both merely disable and the user can't tell which
  // choice is in flight.
  const [pendingChoice, setPendingChoice] = React.useState<'always' | 'never' | null>(null);
  React.useEffect(() => {
    if (!saving) {
      setPendingChoice(null);
    }
  }, [saving]);
  React.useEffect(() => {
    if (!open) {
      setPendingChoice(null);
    }
  }, [open]);
  const pickChoice = (mode: 'always' | 'never') => {
    setPendingChoice(mode);
    void dispatch(confirmAutoStartPrompt(mode));
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) {
          dispatch(cancelAutoStartPrompt());
        }
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            Auto-start {tenantName}/{environmentName}?
          </DialogTitle>
          <DialogDescription>
            This environment runs on an EC2 instance that is currently stopped. Opening it will
            start the instance and may take roughly a minute. Your choice is saved per environment;
            you can change it from the environment settings later.
          </DialogDescription>
        </DialogHeader>
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
            disabled={saving}
            onClick={() => {
              dispatch(cancelAutoStartPrompt());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="secondary"
            disabled={saving}
            onClick={() => {
              pickChoice('never');
            }}
          >
            {pendingChoice === 'never' ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <ShieldOff aria-hidden="true" />
            )}
            Don&apos;t auto-start
          </Button>
          <Button
            type="button"
            disabled={saving}
            onClick={() => {
              pickChoice('always');
            }}
          >
            {pendingChoice === 'always' ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Play aria-hidden="true" />
            )}
            Auto-start
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

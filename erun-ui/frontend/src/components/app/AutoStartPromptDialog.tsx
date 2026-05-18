import { LoaderCircle, Play, ShieldOff } from 'lucide-react';
import * as React from 'react';

import { cancelAutoStartPrompt, confirmAutoStartPrompt } from '@/app/autoStartThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

// AutoStartPromptDialog asks the user once per remote environment whether
// the desktop should start the linked cloud context (and the underlying
// EC2 instance) automatically when they click the env in the sidebar. The
// answer is persisted via SetEnvironmentAutoStart so the dialog does not
// reappear until the user resets the policy from the manage-env dialog.
//
// The two primary actions persist + proceed:
//   - Auto-start  → saves AutoStart=true and re-fires openSelection so the
//                   ERun tab spawns (and erun open's preflight starts EC2).
//   - Don't auto-start → saves AutoStart=false and re-fires openSelection;
//                   navigation still happens, the Local tab still spawns,
//                   but the ERun tab is left out and the titlebar Play
//                   button remains the recovery affordance.
// Cancel just closes the dialog without persisting; nothing else happens,
// matching the no-op state the user could have reached by not clicking.
export function AutoStartPromptDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const prompt = useAppSelector((state) => state.autoStartPrompt);
  const tenantName = prompt.selection?.tenant ?? '';
  const environmentName = prompt.selection?.environment ?? '';
  const saving = prompt.saving;
  const open = prompt.open;
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
          <div className="rounded-[var(--radius)] border border-destructive/40 bg-destructive/10 px-3 py-2.5 text-[13px] leading-[1.4] text-destructive">
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
              void dispatch(confirmAutoStartPrompt('never'));
            }}
          >
            <ShieldOff aria-hidden="true" />
            Don&apos;t auto-start
          </Button>
          <Button
            type="button"
            disabled={saving}
            onClick={() => {
              void dispatch(confirmAutoStartPrompt('always'));
            }}
          >
            {saving ? (
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

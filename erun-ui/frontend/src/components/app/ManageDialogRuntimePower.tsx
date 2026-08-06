import { Power } from 'lucide-react';
import * as React from 'react';

import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import { submitManageStop } from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';

// RuntimePowerField sits directly under Deploy because that is where the
// operator is standing when they find the resource sliders capped: the figures
// below are computed from what the node's pods currently reserve, so stopping an
// environment nobody is using is the action that raises them.
//
// There is deliberately no Start button — opening the environment wakes it
// (`erun open` scales the runtime back up), and a second wake control would be a
// second implementation of the same thing. The helper text names that recovery
// so the stopped state is never shown without the way out of it.
export function RuntimePowerField({
  dialog,
}: {
  dialog: AppState['manageDialog'];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-2">
      <Button
        id="environment-config-stop"
        type="button"
        size="sm"
        variant="outline"
        className="justify-self-start"
        aria-describedby="environment-config-stop-help"
        disabled={dialog.busy || dialog.configLoading}
        onClick={() =>
          void dispatch(submitManageStop()).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          })
        }
      >
        <Power aria-hidden="true" />
        Stop environment
      </Button>
      <p className="text-xs leading-[1.35] text-muted-foreground" id="environment-config-stop-help">
        Scales the runtime to zero and gives its CPU and memory back to the node, so the other
        environments can be given more. Work running in the pod stops; caches, images and the
        worktree are kept. Click the environment in the sidebar to start it again.
      </p>
    </div>
  );
}

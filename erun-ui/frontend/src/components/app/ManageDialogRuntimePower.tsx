import { skipToken } from '@reduxjs/toolkit/query/react';
import { Button } from 'erun-kit';
import { Power } from 'lucide-react';
import * as React from 'react';

import { useGetRuntimeRunStateQuery } from '@/app/api/environmentApi';
import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import { submitManageStop } from '@/app/manageEnvironmentThunks';
import { stopEnvironmentKeptComponents } from '@/app/manageStopThunks';
import { showTerminalError } from '@/app/notificationThunks';
import { summarizeRuntimeRunState } from '@/app/runtimeRunState';
import type { AppState } from '@/app/state';

export const RUNTIME_RUN_STATE_ID = 'environment-config-run-state';
export const RUNTIME_STOP_HELP_ID = 'environment-config-stop-help';

// RuntimePowerField disables Stop when `erun stop` would no-op.
// There is no Start: `erun open` wakes the runtime.
export function RuntimePowerField({
  dialog,
}: {
  dialog: AppState['manageDialog'];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const { data: runState, isFetching } = useGetRuntimeRunStateQuery(dialog.selection ?? skipToken);
  const state = summarizeRuntimeRunState(runState, isFetching);
  const disabled = dialog.busy || dialog.configLoading || state.nothingToStop;

  return (
    <div className="grid gap-2">
      {/* role=status: a stop changes this line just after the click. */}
      <p
        id={RUNTIME_RUN_STATE_ID}
        role="status"
        className="text-xs leading-[1.35] text-muted-foreground"
      >
        <span className="font-medium">{state.label}</span>
        {state.detail ? ` ${state.detail}` : ''}
      </p>
      <Button
        id="environment-config-stop"
        type="button"
        size="sm"
        variant="outline"
        className="justify-self-start"
        aria-describedby={`${RUNTIME_RUN_STATE_ID} ${RUNTIME_STOP_HELP_ID}`}
        disabled={disabled}
        onClick={() =>
          void dispatch(submitManageStop()).catch((error: unknown) => {
            dispatch(showTerminalError(readError(error)));
          })
        }
      >
        <Power aria-hidden="true" />
        Stop environment
      </Button>
      <p className="text-xs leading-[1.35] text-muted-foreground" id={RUNTIME_STOP_HELP_ID}>
        {stopHelpText(stopEnvironmentKeptComponents(runState?.remainingComponents))}
      </p>
    </div>
  );
}

// stopHelpText names the components a stop leaves running: it scales only the runtime Deployment.
function stopHelpText(keptComponents: string): string {
  const scope =
    keptComponents === ''
      ? "This stops the runtime only: platform components deployed into this environment (added with 'erun deploy --components' — the API, its database) are not part of it and are not stopped, so they keep running and keep holding their capacity."
      : `This stops the runtime only. Platform components deployed into this environment are not part of it and are not stopped, so these keep running and keep holding their capacity: ${keptComponents}.`;
  return (
    "Scales this environment's runtime — the pod, and the dind sidecar builds run in — to zero, and " +
    'gives their CPU and memory back to the node so the other environments can be given more. ' +
    `Work running in the pod stops; caches, images and the worktree are kept. ${scope} ` +
    'Click the environment in the sidebar to start it again.'
  );
}

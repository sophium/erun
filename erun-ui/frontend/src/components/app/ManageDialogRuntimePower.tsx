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

// RuntimePowerField sits directly under Deploy because that is where the
// operator is standing when they find the resource sliders capped: the figures
// below are computed from what the node's pods currently reserve, so stopping an
// environment nobody is using is the action that raises them.
//
// It leads with the runtime's run state, and disables Stop when there is
// nothing to stop. The state is what `erun stop` itself reads to decide its own
// no-op, so without it the control offers a correct no-op as if it were the
// action that frees capacity — the operator presses it, the dialog does not
// change, and the button reads as broken. The state line is also the answer to
// the other shape of that defect, a runtime that was never deployed, which
// `erun stop` refuses outright.
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
  const { data: runState, isFetching } = useGetRuntimeRunStateQuery(dialog.selection ?? skipToken);
  const state = summarizeRuntimeRunState(runState, isFetching);
  const disabled = dialog.busy || dialog.configLoading || state.nothingToStop;

  return (
    <div className="grid gap-2">
      {/* Above the button, not below it: whether pressing Stop will do anything
          is what the operator needs before they decide to press it, and the
          reason a disabled control is disabled has to be reachable without
          activating it. role=status because a stop this desktop issues changes
          this line a moment after the click. */}
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

// stopHelpText states Stop's scope. A stop scales the environment's runtime
// Deployment and nothing else, so the components an operator rolled out
// alongside it (`erun deploy --components`: the platform's API, its database,
// the ingress and DNS pieces) keep running and keep holding their capacity.
// That is deliberate — on an environment hosting the platform those components
// are the platform — but the panel they read before clicking promised the
// figure would rise, and it cannot while they are up. Naming them, before the
// click, is what keeps them from being read afterwards as a stop that failed.
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

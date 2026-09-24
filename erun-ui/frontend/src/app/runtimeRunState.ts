import type { UIRuntimeRunState } from '@/uiRuntimeTypes';

// RuntimeRunStateSummary is the Runtime tab's reading of the environment's
// runtime Deployment, in the terms the Stop control needs: what state the
// runtime is in, and whether pressing Stop would do anything at all.
//
// The second half is the point. Stop is a real action only when the Deployment
// currently wants pods — `erun stop` reads exactly these replica counts and
// reports "already stopped" when it finds zero — so a control that never
// consults them offers a correct no-op as if it were the thing that frees the
// node's capacity. nothingToStop is what turns that into a disabled control
// with a stated reason instead of a button whose only feedback is a terminal
// line the operator may not be looking at.
export interface RuntimeRunStateSummary {
  // label states the runtime's condition, ending without punctuation so a
  // detail sentence can follow it.
  label: string;
  // detail supports the label in one sentence, '' when the label is the whole
  // answer.
  detail: string;
  // nothingToStop is true only when the cluster positively reported there is
  // nothing for Stop to do. It is false while the state is unknown: an
  // unreadable cluster is a diagnostic problem, and disabling the action on one
  // would block the only control the operator has.
  nothingToStop: boolean;
  // checking covers the window before the read answers, which is deliberately
  // neither of the two states above.
  checking: boolean;
}

// RUNTIME_RUN_STATE_UNREAD labels the failed read. It states what could not be
// read rather than borrowing a state nobody observed: "not deployed" and "could
// not be read" are different facts, and only the first is something the
// operator can act on by deploying.
export const RUNTIME_RUN_STATE_UNREAD = 'Runtime run state unread';

// summarizeRuntimeRunState reduces the run-state read to the line the Stop
// control renders above its button.
export function summarizeRuntimeRunState(
  state: UIRuntimeRunState | undefined,
  loading: boolean,
): RuntimeRunStateSummary {
  if (!state) {
    return {
      label: loading ? 'Checking whether this runtime is running…' : RUNTIME_RUN_STATE_UNREAD,
      detail: '',
      nothingToStop: false,
      checking: loading,
    };
  }
  if (state.message) {
    return {
      label: RUNTIME_RUN_STATE_UNREAD,
      detail: state.message,
      nothingToStop: false,
      checking: false,
    };
  }
  if (!state.present) {
    return {
      label: 'Runtime not deployed',
      detail: 'Nothing to stop — there is no runtime Deployment for this environment yet.',
      nothingToStop: true,
      checking: false,
    };
  }
  if (state.stopped) {
    return {
      label: 'Runtime stopped',
      detail: 'Nothing to stop — the runtime already wants no pods.',
      nothingToStop: true,
      checking: false,
    };
  }
  return {
    label: `Runtime running — ${String(state.readyReplicas)} of ${String(state.desiredReplicas)} ready`,
    detail: '',
    nothingToStop: false,
    checking: false,
  };
}

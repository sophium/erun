import type { UIRuntimeRunState } from '@/uiRuntimeTypes';

// RuntimeRunStateSummary is the Runtime tab's reading of the runtime Deployment
// for the Stop control.
export interface RuntimeRunStateSummary {
  // label has no trailing punctuation so detail can follow it.
  label: string;
  detail: string;
  // nothingToStop is false while the state is unknown, so an unreadable cluster
  // never disables Stop.
  nothingToStop: boolean;
  checking: boolean;
}

export const RUNTIME_RUN_STATE_UNREAD = 'Runtime run state unread';

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

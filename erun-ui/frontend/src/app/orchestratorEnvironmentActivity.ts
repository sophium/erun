import type { OrchestratorEnvRef } from '@/app/slices/orchestratorsSlice';
import type { StatusDotState } from '@/components/app/Sidebar.StatusDot';

// orchestratorEnvironmentLine reduces one linked environment's joined
// activity (environment_activity.go's poller, via OrchestratorEnvRef.activity)
// to the one line the hover card renders for it. The reduction is deliberately
// not a single boolean: outage and unreachable read as the same "nothing here"
// to a naive summary, but they are different operator situations (a forward
// that died vs. one that was never opened), and observed=false must not
// collapse into idle just because busy is also false -- that would report a
// state nobody actually confirmed.
export type OrchestratorEnvironmentState =
  | 'outage'
  | 'unreachable'
  | 'unknown'
  | 'awaiting-input'
  | 'busy'
  | 'idle';

export interface OrchestratorEnvironmentLine {
  key: string;
  name: string;
  state: OrchestratorEnvironmentState;
  status: string;
  // dot is omitted for the states with no shared StatusDotGlyph shape
  // (unreachable, unknown, awaiting-input) rather than reusing 'stopped' or
  // 'failed' for them and losing the distinction those exist to preserve.
  dot?: StatusDotState;
}

export function orchestratorEnvironmentLine(env: OrchestratorEnvRef): OrchestratorEnvironmentLine {
  const key = `${env.tenant}/${env.environment}`;
  const name = `${env.tenant} / ${env.environment}`;
  const activity = env.activity;

  if (activity?.outage) {
    return { key, name, state: 'outage', status: 'Lost connection', dot: 'failed' };
  }
  if (!activity?.reachable) {
    return { key, name, state: 'unreachable', status: 'Not open here' };
  }
  if (!activity.observed) {
    return { key, name, state: 'unknown', status: 'Connected — activity unknown' };
  }
  // The Agent's own structured report (never inferred from output volume or
  // timing) is checked ahead of the generic marker-based busy/idle split: an
  // AI session waiting on the operator is the state this whole model exists
  // to surface, and it can be true even while the generic markers read idle.
  if (activity.aiState === 'awaiting-input') {
    return { key, name, state: 'awaiting-input', status: 'Agent is waiting for your input' };
  }
  if (activity.busy || activity.aiState === 'busy') {
    return {
      key,
      name,
      state: 'busy',
      status: activity.detail ? `Busy — ${activity.detail}` : 'Busy',
      dot: 'busy',
    };
  }
  return { key, name, state: 'idle', status: 'Idle', dot: 'running' };
}

import { summarizeEnvironmentUsage } from '@/app/environmentUsageSummary';
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
export type OrchestratorEnvironmentState = 'outage' | 'unreachable' | 'unknown' | 'busy' | 'idle';

export interface OrchestratorEnvironmentLine {
  key: string;
  name: string;
  state: OrchestratorEnvironmentState;
  status: string;
  // dot is omitted for the two states with no shared StatusDotGlyph shape
  // (unreachable, unknown) rather than reusing 'stopped' or 'failed' for them
  // and losing the distinction those two exist to preserve.
  dot?: StatusDotState;
  // usage is the compact CPU/memory figure from the usage sweep's cached
  // reading for this env (environment_usage.go), '' when there is nothing
  // measurable to show yet — the orchestrator card is where #1383 actually
  // asked for this.
  usage: string;
  usageStale: boolean;
}

export function orchestratorEnvironmentLine(env: OrchestratorEnvRef): OrchestratorEnvironmentLine {
  const key = `${env.tenant}/${env.environment}`;
  const name = `${env.tenant} / ${env.environment}`;
  const activity = env.activity;
  const usageSummary = summarizeEnvironmentUsage(env.usage, Date.now());
  const usage = usageSummary.headline;
  const usageStale = usageSummary.stale;

  if (activity?.outage) {
    return {
      key,
      name,
      state: 'outage',
      status: 'Lost connection',
      dot: 'failed',
      usage,
      usageStale,
    };
  }
  if (!activity?.reachable) {
    return { key, name, state: 'unreachable', status: 'Not open here', usage, usageStale };
  }
  if (!activity.observed) {
    return {
      key,
      name,
      state: 'unknown',
      status: 'Connected — activity unknown',
      usage,
      usageStale,
    };
  }
  if (activity.busy) {
    return {
      key,
      name,
      state: 'busy',
      status: activity.detail ? `Busy — ${activity.detail}` : 'Busy',
      dot: 'busy',
      usage,
      usageStale,
    };
  }
  return { key, name, state: 'idle', status: 'Idle', dot: 'running', usage, usageStale };
}

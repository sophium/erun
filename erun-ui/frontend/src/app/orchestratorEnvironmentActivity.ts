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
// state nobody actually confirmed. checkFailed is the same split applied to an
// environment with no local forward: the poller still asks it directly (over
// its own runtime pod), so a failed attempt to answer is its own state, not a
// re-use of "unreachable" — an environment nobody opened here can be busy
// right now, so "unreachable" must stay reserved for one nothing has ever
// asked.
export type OrchestratorEnvironmentState =
  | 'outage'
  | 'unreachable'
  | 'check-failed'
  | 'unknown'
  | 'busy'
  | 'idle';

export interface OrchestratorEnvironmentLine {
  key: string;
  name: string;
  state: OrchestratorEnvironmentState;
  status: string;
  // dot is omitted for the three states with no shared StatusDotGlyph shape
  // (unreachable, check-failed, unknown) rather than reusing 'stopped' or
  // 'failed' for them and losing the distinction those exist to preserve.
  dot?: StatusDotState;
  // usage is the compact CPU/memory figure from the usage sweep's cached
  // reading for this env (environment_usage.go), '' when there is nothing
  // measurable to show yet — the orchestrator card is where the orchestrator card actually
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
  if (activity?.checkFailed) {
    // Not the same silence as never asking: this desktop has no local
    // forward, so it asked the environment directly instead, and the attempt
    // itself did not come back. Naming the action (open it) is what the plain
    // "Not open here" line below cannot do for an environment that might be
    // busy right now and simply could not confirm it from here.
    return {
      key,
      name,
      state: 'check-failed',
      status: "Can't confirm from here — open it to check directly",
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

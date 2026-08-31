import { summarizeEnvironmentUsage } from '@/app/environmentUsageSummary';
import type { OrchestratorEnvRef } from '@/app/slices/orchestratorsSlice';
import type { StatusDotState } from '@/components/app/Sidebar.StatusDot';

// orchestratorEnvironmentLine reduces one linked environment's joined
// activity (environment_activity.go's poller, via OrchestratorEnvRef.activity)
// to the one line the hover card renders for it. The reduction is deliberately
// not a single boolean: outage and no-forward read as the same "nothing here"
// to a naive summary, but they are different operator situations (a forward
// that died vs. one that was never opened), and observed=false must not
// collapse into idle just because busy is also false -- that would report a
// state nobody actually confirmed. checkFailed is the same split applied to an
// environment with no local forward: the poller still asks it directly (over
// its own runtime pod), so a failed attempt to answer is its own state, not a
// re-use of "no-forward" — an environment nobody opened here can be busy
// right now, so "no-forward" must stay reserved for one nothing has ever
// asked.
//
// Every field this reduces is the *desktop's* observation (see
// environment_activity.go's own doc comment) -- this card is titled with the
// orchestrator, not the desktop, and an orchestrator is a host-side session
// with its own MCP client and its own connections, independent of whatever
// local forward this desktop happens to have open. So no branch here may
// claim anything about the *orchestrator's* reach: "no-forward" states only
// what the desktop itself could establish, never that the environment is
// closed to anyone else, and its wording says "this desktop" rather than a
// bare "not open" for exactly that reason. checkFailed already gets this
// right — it says "can't confirm", not "unreachable" — because a
// real attempt that came back empty is honest uncertainty, not a confident
// negative; no-forward is the same idea applied to the case where the desktop
// never had a channel to ask through at all.
export type OrchestratorEnvironmentState =
  | 'outage'
  | 'no-forward'
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
  // (no-forward, check-failed, unknown) rather than reusing 'stopped' or
  // 'failed' for them and losing the distinction those exist to preserve.
  dot?: StatusDotState;
  // usage is the compact CPU/memory figure from the usage sweep's cached
  // reading for this env (environment_usage.go), '' when there is nothing
  // measurable to show yet — the orchestrator card is where the orchestrator card actually
  // asked for this.
  usage: string;
  usageStale: boolean;
  // roleLabel names what this orchestrator uses the environment for ("Code
  // role", "Build role", "Runtime role"), '' when undeclared — undeclared
  // stays silent rather than rendering "Undeclared role" on every row that
  // predates this field. This is what makes an operate-role link's
  // association visible in the card, the same way it is for any other role.
  roleLabel: string;
}

// orchestratorEnvRoleCaption renders the short label the hover card shows
// beneath an environment's status line. '' for undeclared, matching
// erun-cli/cmd/list.go's own "say nothing rather than guess" treatment of an
// unset role.
function orchestratorEnvRoleCaption(role: OrchestratorEnvRef['role']): string {
  switch (role) {
    case 'code':
      return 'Code role';
    case 'build':
      return 'Build role';
    case 'runtime':
      return 'Runtime role';
    default:
      return '';
  }
}

export function orchestratorEnvironmentLine(env: OrchestratorEnvRef): OrchestratorEnvironmentLine {
  const key = `${env.tenant}/${env.environment}`;
  const name = `${env.tenant} / ${env.environment}`;
  const activity = env.activity;
  const usageSummary = summarizeEnvironmentUsage(env.usage, Date.now());
  const usage = usageSummary.headline;
  const usageStale = usageSummary.stale;
  const roleLabel = orchestratorEnvRoleCaption(env.role);

  if (activity?.outage) {
    return {
      key,
      name,
      state: 'outage',
      status: 'Lost connection',
      dot: 'failed',
      usage,
      usageStale,
      roleLabel,
    };
  }
  if (activity?.checkFailed) {
    // Not the same silence as never asking: this desktop has no local
    // forward, so it asked the environment directly instead, and the attempt
    // itself did not come back. Naming the action (open it) is what the plain
    // "no forward" line below cannot do for an environment that might be busy
    // right now and simply could not be confirmed from this desktop.
    return {
      key,
      name,
      state: 'check-failed',
      status: "Can't confirm from here — open it to check directly",
      usage,
      usageStale,
      roleLabel,
    };
  }
  if (!activity?.reachable) {
    // The desktop has no local forward to this environment and no other
    // channel came back with an answer either -- it genuinely has nothing to
    // report, not a confirmed "closed" (that would be checkFailed above, or
    // outage). Naming "this desktop" keeps the line honest: an orchestrator is
    // a separate host-side client, so its own reach is unknown from this
    // reading, never asserted false by it.
    return {
      key,
      name,
      state: 'no-forward',
      status: 'No forward from this desktop',
      usage,
      usageStale,
      roleLabel,
    };
  }
  if (!activity.observed) {
    return {
      key,
      name,
      state: 'unknown',
      status: 'Connected — activity unknown',
      usage,
      usageStale,
      roleLabel,
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
      roleLabel,
    };
  }
  return { key, name, state: 'idle', status: 'Idle', dot: 'running', usage, usageStale, roleLabel };
}

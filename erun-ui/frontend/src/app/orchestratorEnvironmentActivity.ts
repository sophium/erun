import { summarizeEnvironmentUsage } from '@/app/environmentUsageSummary';
import type { OrchestratorEnvRef } from '@/app/slices/orchestratorsSlice';
import type { StatusDotState } from '@/components/app/Sidebar.StatusDot';

// OrchestratorEnvironmentState reflects only this desktop's observation, so no
// state may claim anything about the orchestrator's own reach.
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
  // Omitted for states with no matching StatusDotGlyph, rather than reusing one.
  dot?: StatusDotState;
  // '' when there is nothing measurable yet.
  usage: string;
  usageStale: boolean;
  // '' when undeclared.
  roleLabel: string;
}

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
    // Asked directly and got no answer: it may be busy, so name the action.
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
    // No forward and no answer: scoped to this desktop, never a confirmed "closed".
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

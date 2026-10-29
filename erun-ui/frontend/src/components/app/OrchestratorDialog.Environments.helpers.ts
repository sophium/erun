import type { SelectFieldOption } from 'erun-kit';

import { environmentTypeIsHost } from '@/app/environmentType';
import type { OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';

export interface EnvCandidate {
  tenant: string;
  environment: string;
  // Resolved env type; decides the offered roles and the directory label.
  environmentType: string;
  // Ineligible envs are listed disabled with ineligibleReason, not dropped.
  eligible: boolean;
  defaultDirectory: string;
  // A mirrored env is reviewed in a synced copy placed anywhere; otherwise the path derives from the env.
  mirrored: boolean;
  // Set when only one role is allowed (a runtime env, which carries no directory); absent does not mean every role works.
  requiredRole?: OrchestratorEnvRole;
  ineligibleReason: string;
}

export function envKey(tenant: string, environment: string): string {
  return `${tenant} ${environment}`;
}

// envRoleFieldId dash-joins because a space-joined envKey is not a safe CSS id.
export function envRoleFieldId(tenant: string, environment: string): string {
  return `orchestrator-env-role-${tenant}-${environment}`;
}

// OrchestratorEnvRoleOptionValue uses 'none' for undeclared, since Radix Select rejects ''.
// A closed union keeps the label lookup total under noUncheckedIndexedAccess.
export type OrchestratorEnvRoleOptionValue = 'none' | Exclude<OrchestratorEnvRole, ''>;

// ORCHESTRATOR_ENV_ROLE_LABELS is the operator-facing word for each role value.
export const ORCHESTRATOR_ENV_ROLE_LABELS: Record<OrchestratorEnvRoleOptionValue, string> = {
  none: 'Not declared',
  code: 'Code',
  build: 'Build',
  runtime: 'Runtime',
};

// orchestratorEnvRoleOptions matches eruncommon.OrchestratorEnvRoleAllowed exactly; a host env takes no runtime role.
export function orchestratorEnvRoleOptions(
  candidate: EnvCandidate,
  role: OrchestratorEnvRole,
): SelectFieldOption[] {
  const options: OrchestratorEnvRoleOptionValue[] = ['none', 'code', 'build'];
  if (!environmentTypeIsHost(candidate.environmentType)) {
    options.push('runtime');
  }
  // Keep a stored role the gate now refuses visible.
  if (role !== '' && !options.includes(role)) {
    options.push(role);
  }
  return options.map((value) => ({ value, label: ORCHESTRATOR_ENV_ROLE_LABELS[value] }));
}

// candidateDirectoryLabel says where code lives; a host env has a directory, not a pod's worktree.
export function candidateDirectoryLabel(
  candidate: EnvCandidate,
  operatedDirectly: boolean,
): string {
  if (operatedDirectly) {
    return 'operated directly — no review directory';
  }
  if (candidate.mirrored) {
    return 'synced mirror';
  }
  return environmentTypeIsHost(candidate.environmentType)
    ? 'directory on this machine'
    : 'worktree on this machine';
}

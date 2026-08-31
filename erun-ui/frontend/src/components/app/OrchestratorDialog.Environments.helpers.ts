import type { OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';

export interface EnvCandidate {
  tenant: string;
  environment: string;
  // An ineligible env (one whose type isn't recognized at all) is still
  // listed, disabled, with ineligibleReason set, rather than silently
  // dropped: an operator who knows the env exists must be able to see that
  // it was considered and why it can't be linked.
  eligible: boolean;
  defaultDirectory: string;
  // A mirrored env is reviewed in a synced copy the operator may place anywhere;
  // otherwise the review directory is the env's own worktree on this machine and
  // its path is derived from the env, not chosen here.
  mirrored: boolean;
  // requiredRole is the one role this candidate may be linked with, absent
  // (the Go side omits it — omitempty) when any role, including undeclared,
  // works. A runtime environment sets this to 'runtime': it has no worktree
  // to review and no in-pod agent to delegate to, so it carries no directory
  // (defaultDirectory is '' and mirrored is false) and the role picker
  // offers only that one choice instead of the mirror/worktree directory
  // controls, which have nothing to show for a link with no review
  // directory.
  requiredRole?: OrchestratorEnvRole;
  ineligibleReason: string;
}

export function envKey(tenant: string, environment: string): string {
  return `${tenant} ${environment}`;
}

// envRoleFieldId is the role SelectField's DOM id for one candidate row. A
// plain envKey (space-joined) is not safe as a raw CSS id -- unescaped in a
// selector, the space would end the id token early -- so this uses a
// dash-joined id instead, which is what OrchestratorDialog.spec.ts locates
// the trigger by.
export function envRoleFieldId(tenant: string, environment: string): string {
  return `orchestrator-env-role-${tenant}-${environment}`;
}

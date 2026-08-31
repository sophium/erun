export interface EnvCandidate {
  tenant: string;
  environment: string;
  // An ineligible env (e.g. runtime — no worktree to review, no in-pod agent
  // to delegate to) is still listed, disabled, with ineligibleReason set,
  // rather than silently dropped: an operator who knows the env exists must
  // be able to see that it was considered and why it can't be linked.
  eligible: boolean;
  defaultDirectory: string;
  // A mirrored env is reviewed in a synced copy the operator may place anywhere;
  // otherwise the review directory is the env's own worktree on this machine and
  // its path is derived from the env, not chosen here.
  mirrored: boolean;
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

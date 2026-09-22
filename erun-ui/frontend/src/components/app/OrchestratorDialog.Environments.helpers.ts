import type { SelectFieldOption } from 'erun-kit';

import { environmentTypeIsHost } from '@/app/environmentType';
import type { OrchestratorEnvRole } from '@/app/slices/orchestratorsSlice';

export interface EnvCandidate {
  tenant: string;
  environment: string;
  // environmentType is the env's resolved type ('local-agent', 'remote-agent',
  // 'runtime', 'host'). Carried so the dialog reasons about a candidate the way
  // eruncommon.OrchestratorEnvRoleAllowed does rather than re-deriving it: it
  // decides which roles the picker offers (a host env takes no runtime role)
  // and which words describe the directory row.
  environmentType: string;
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
  // (the Go side omits it — omitempty) when no single role is required. Absent
  // does not mean every role works: the picker still offers only the roles the
  // environment's type allows, so a host candidate's absent requiredRole still
  // excludes runtime. A runtime environment sets this to 'runtime': it has no worktree
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

// OrchestratorEnvRoleOptionValue is every value the role picker can carry: the
// real OrchestratorEnvRole values minus undeclared, plus the 'none' sentinel
// that stands in for undeclared. Radix's Select.Item rejects an empty-string
// value, so '' is represented here as 'none' and translated back at the
// boundary -- this list and the round trip in EnvironmentRowRole are the only
// two places that need to know about it. Typed as a closed union rather than
// plain string so the label lookup below is total under
// noUncheckedIndexedAccess, where indexing a Record<string, string> would be
// string | undefined.
export type OrchestratorEnvRoleOptionValue = 'none' | Exclude<OrchestratorEnvRole, ''>;

// ORCHESTRATOR_ENV_ROLE_LABELS is the operator-facing word for each role value.
export const ORCHESTRATOR_ENV_ROLE_LABELS: Record<OrchestratorEnvRoleOptionValue, string> = {
  none: 'Not declared',
  code: 'Code',
  build: 'Build',
  runtime: 'Runtime',
};

// orchestratorEnvRoleOptions lists the roles this candidate may be linked
// with, and it is deliberately the same set eruncommon.OrchestratorEnvRoleAllowed
// accepts -- no wider (a role the gate refuses must not be offerable) and no
// narrower (an off-list role the config already holds renders a blank trigger,
// since no item matches its value, and the operator cannot change it back).
// A host env takes no runtime role: "operate directly" means deploy, pin,
// observe, and a host env has no pod for any of those to act on.
export function orchestratorEnvRoleOptions(
  candidate: EnvCandidate,
  role: OrchestratorEnvRole,
): SelectFieldOption[] {
  const options: OrchestratorEnvRoleOptionValue[] = ['none', 'code', 'build'];
  if (!environmentTypeIsHost(candidate.environmentType)) {
    options.push('runtime');
  }
  // A config.yaml edited by hand can still hold a role the gate would now
  // refuse, and Edit mode must show the operator what is actually stored.
  if (role !== '' && !options.includes(role)) {
    options.push(role);
  }
  return options.map((value) => ({ value, label: ORCHESTRATOR_ENV_ROLE_LABELS[value] }));
}

// candidateDirectoryLabel names, in three words, where this candidate's code
// lives — the one thing an operator needs to tell two similar-looking rows
// apart. A host env is called a directory rather than a worktree on purpose: a
// worktree here is a pod's, hostPath-mounted into it, and a host env has no pod
// at all, so the same words would describe two different relationships.
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

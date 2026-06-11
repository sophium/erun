import type { ManageTab, UIEnvironmentConfig } from '@/types';

export function cloneEnvironmentConfig(config: UIEnvironmentConfig): UIEnvironmentConfig {
  return JSON.parse(JSON.stringify(config)) as UIEnvironmentConfig;
}

// compactClaudeDraft drops the unset fields from a merged Claude draft so the
// dialog state only carries explicit overrides — an absent key means "use the
// default", and the backend omits absent fields when persisting.
export function compactClaudeDraft(
  merged: UIEnvironmentConfig['claude'],
): UIEnvironmentConfig['claude'] {
  const next: UIEnvironmentConfig['claude'] = {};
  if (merged.useMantle !== undefined) next.useMantle = merged.useMantle;
  if (merged.useBedrock !== undefined) next.useBedrock = merged.useBedrock;
  if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
  if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
  if (merged.effort !== undefined) next.effort = merged.effort;
  if (merged.defaultModel !== undefined) next.defaultModel = merged.defaultModel;
  if (merged.verboseDebug !== undefined) next.verboseDebug = merged.verboseDebug;
  return next;
}

// aiSessionLaunchSignature distills the env config down to what changes the
// AI tab's Claude launch command, mirroring the resolution that erun-common's
// AISessionLaunchCommand (the Go owner of the real command) applies:
// --effort / --model / --verbose --debug compose from the Claude config, the
// model counting only while it is among the env's available models. Saving a
// config whose signature changed must reopen the env's open AI tabs — a
// launch flag only takes effect when the persistent session's program starts
// (issues #477/#482). Envs whose AI tool launches verbatim (non-claude) are
// filtered backend-side by EndAISessions, which knows the tool.
export function aiSessionLaunchSignature(config: UIEnvironmentConfig): string {
  const claude = config.claude;
  const available =
    (claude.models?.length ?? 0) > 0 ? (claude.models ?? []) : config.claudeDefaults.models;
  const model =
    claude.defaultModel && available.includes(claude.defaultModel) ? claude.defaultModel : '';
  return [claude.effort ?? '', model, claude.verboseDebug === true ? 'verbose-debug' : ''].join(
    ' ',
  );
}

// nextPendingRedeploy reports whether the pending-redeploy banner should be
// up after a save: it stays up once raised (a later metadata-only save must
// not clear a redeploy the user still owes the pod), and a save raises it
// only when it changed a pod-shaping field (issue #460). A missing prior
// config means the diff cannot be computed, so claim the redeploy — the
// conservative direction.
export function nextPendingRedeploy(
  alreadyPending: boolean,
  prior: UIEnvironmentConfig | null,
  saved: UIEnvironmentConfig,
): boolean {
  if (alreadyPending || !prior) {
    return true;
  }
  return deployRelevantSignature(prior) !== deployRelevantSignature(saved);
}

// deployRelevantSignature distills the env config down to the fields that
// shape the running pod — the values erun-common/deploy.go renders into the
// Helm release (pod resources, idle.* pod env, the claude.* pod env subset)
// plus the image registry/channel and the cloud binding the deploy resolves
// against. Fields that never reach the pod stay out: autoUpgrade /
// upgradeChannel select a future `erun upgrade` run, autoStart and
// remoteHostCredentials are desktop-side behaviour, sshd.workspaceSync* is
// desktop sync, and claude effort/defaultModel/verboseDebug only change the
// AI launch command (the save path relaunches AI tabs for those). A save
// whose signature is unchanged must not raise the pending-redeploy banner
// (issue #460).
function deployRelevantSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    containerRegistry: config.containerRegistry,
    cloudProviderAlias: config.cloudProviderAlias,
    snapshot: config.snapshot,
    runtimePod: config.runtimePod,
    idle: config.idle,
    claudePod: {
      useMantle: config.claude.useMantle,
      useBedrock: config.claude.useBedrock,
      models: config.claude.models,
      maxOutputTokens: config.claude.maxOutputTokens,
    },
  });
}

export function manageDialogTabHasUnsavedChanges(
  tab: ManageTab,
  config: UIEnvironmentConfig,
  initial: UIEnvironmentConfig | null,
): boolean {
  if (!initial) {
    return false;
  }
  const compare = (...keys: (keyof UIEnvironmentConfig)[]): boolean =>
    keys.some((key) => JSON.stringify(config[key]) !== JSON.stringify(initial[key]));
  switch (tab) {
    case 'general':
      return compare(
        'containerRegistry',
        'cloudProviderAlias',
        'snapshot',
        'remoteHostCredentials',
      );
    case 'runtime':
      return compare('runtimePod', 'idle', 'autoStart', 'autoUpgrade', 'upgradeChannel');
    case 'ai':
      return compare('claude');
    case 'ports':
      return false;
    case 'ssh':
      return (
        JSON.stringify(config.sshd.workspaceSyncEnabled) !==
          JSON.stringify(initial.sshd.workspaceSyncEnabled) ||
        JSON.stringify(config.sshd.workspaceSyncLocalPath) !==
          JSON.stringify(initial.sshd.workspaceSyncLocalPath)
      );
    case 'history':
      // History is read-only — no edits, no save, never dirty.
      return false;
    case 'delete':
      return false;
  }
}

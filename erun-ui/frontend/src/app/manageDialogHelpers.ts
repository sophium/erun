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

// aiSessionLaunchSignature distills the env config down to what changes the AI
// tab's Claude launch command, mirroring the resolution erun-common's
// AISessionLaunchCommand (the Go owner) applies. A save whose signature changed
// must reopen the env's open AI tabs — a launch flag only takes effect when the
// persistent session restarts. Non-claude tools launch verbatim and are
// filtered backend-side by EndAISessions.
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
// only when it changed a pod-shaping field. A missing prior
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

// deployRelevantSignature captures only the fields a redeploy would apply, so a
// metadata-only save does not raise the pending-redeploy banner. The other
// fields deliberately stay out because a redeploy would not touch them:
// autoUpgrade/upgradeChannel select a future `erun upgrade`, autoStart and
// remoteHostCredentials and sshd sync are desktop-side, and claude
// effort/defaultModel/verboseDebug only change the AI launch command (the save
// path relaunches AI tabs for those instead).
function deployRelevantSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    // A local-agent env's worktree hostPath is its LocalRepoPath, so retargeting
    // it changes what the next deploy mounts — deploy-relevant.
    localRepoPath: config.localRepoPath,
    containerRegistries: config.containerRegistries,
    disableBuildScript: config.disableBuildScript,
    // Mounting source flips a runtime env's worktree onto a PVC and makes the pod
    // clone repoURL at the release ref, so both change what a redeploy provisions.
    mountSource: config.mountSource,
    repoURL: config.repoURL,
    cloudProviderAlias: config.cloudProviderAlias,
    // Cloudflare (and any non-AWS) alias attachments are delivered into the pod
    // at deploy time via a chart Secret, so changing a slot is deploy-relevant.
    cloudAliasSlots: config.cloudAliasSlots,
    type: config.type,
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
        'localRepoPath',
        'containerRegistries',
        'cloudProviderAlias',
        'cloudAliasSlots',
        'remoteHostCredentials',
        'type',
      );
    case 'runtime':
      return compare(
        'runtimePod',
        'idle',
        'autoStart',
        'autoUpgrade',
        'upgradeChannel',
        'disableBuildScript',
        'mountSource',
        'repoURL',
      );
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

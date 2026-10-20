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
  // In the Go JSON order (ui_model.go uiClaudeConfig): the AI tab's dirty check
  // compares serialized drafts, so a key appended out of order would mark a
  // loaded override as an unsaved change.
  if (merged.useGateway !== undefined) next.useGateway = merged.useGateway;
  if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
  if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
  if (merged.effort !== undefined) next.effort = merged.effort;
  if (merged.defaultModel !== undefined) next.defaultModel = merged.defaultModel;
  if (merged.verboseDebug !== undefined) next.verboseDebug = merged.verboseDebug;
  return next;
}

// aiSessionLaunchSignature is the launch command the env's AI tab runs for a
// config, as the backend resolved it (erun-common AISessionLaunchCommand). A
// save whose signature changed must reopen the env's open AI tabs — a launch
// flag only takes effect when the persistent session restarts. It is read from
// the backend rather than re-derived here because the launch resolves the
// erun-level gateway, the env's opt-out, the catalog's models and Remote
// Control together, and a copy of those rules in the frontend is what let a
// gateway change go unapplied. Non-claude tools launch verbatim and are
// filtered backend-side by EndAISessions.
export function aiSessionLaunchSignature(config: UIEnvironmentConfig): string {
  return config.aiLaunchSignature;
}

// versionSourceSignature captures what decides which registries the version
// picker lists from, mirroring the backend's discovery: the env's own marked
// registries, and — for an env that marks none — the local repo path whose
// project config supplies them instead. Roles stay out because discovery reads
// the hosts, not what each one is for, and a cluster entry names no host at all.
// A save that changes this must re-query the picker, or a registry the operator
// just added contributes neither versions nor a notice.
export function versionSourceSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    registries: config.containerRegistries
      .map((entry) => entry.registry.trim())
      .filter((registry) => registry !== ''),
    localRepoPath: config.localRepoPath,
  });
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
// path relaunches AI tabs for those instead). useGateway is deploy-relevant as
// well as a launch input: it decides whether the chart renders the gateway's
// base URL, models and credential reference, and whether deploy applies or
// prunes the gateway credential Secret.
function deployRelevantSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    // A local-agent env's worktree hostPath is its LocalRepoPath, so retargeting
    // it changes what the next deploy mounts — deploy-relevant.
    localRepoPath: config.localRepoPath,
    containerRegistries: config.containerRegistries,
    disableBuildScript: config.disableBuildScript,
    // The chart the runtime is installed from is one of the deploy's coordinates,
    // so changing it changes what a redeploy installs.
    runtimeChart: config.runtimeChart,
    // Platform account flips the runtime SA's cluster RBAC (a <release>-platform
    // ClusterRoleBinding to cluster-admin), which the next deploy renders/prunes.
    platformAccount: config.platformAccount,
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
      useGateway: config.claude.useGateway,
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
  // A table rather than a switch: every tab that edits config names the keys it
  // owns, and a tab absent from the table is read-only by construction. Adding a
  // tab is then a data change, not another branch.
  const editedKeys: Partial<Record<ManageTab, (keyof UIEnvironmentConfig)[]>> = {
    general: [
      'localRepoPath',
      'containerRegistries',
      'runtimeRegistry',
      'imagePullSecrets',
      'cloudProviderAlias',
      'cloudAliasSlots',
      'remoteHostCredentials',
      'type',
    ],
    runtime: [
      'runtimePod',
      'idle',
      'autoStart',
      'autoUpgrade',
      'upgradeChannel',
      'disableBuildScript',
      'runtimeChart',
      'platformAccount',
      'mountSource',
      'repoURL',
    ],
    ai: ['claude'],
    // Only the two workspace-sync values are editable here; the rest of the SSH
    // section is applied by its own actions rather than the dialog's Save.
    ssh: ['sshd'],
  };
  const keys = editedKeys[tab];
  if (!keys) {
    return false;
  }
  if (tab === 'ssh') {
    return (
      JSON.stringify(config.sshd.workspaceSyncEnabled) !==
        JSON.stringify(initial.sshd.workspaceSyncEnabled) ||
      JSON.stringify(config.sshd.workspaceSyncLocalPath) !==
        JSON.stringify(initial.sshd.workspaceSyncLocalPath)
    );
  }
  return compare(...keys);
}

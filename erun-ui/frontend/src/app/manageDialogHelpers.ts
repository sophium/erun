import type { ManageTab, UIEnvironmentConfig } from '@/types';

export function cloneEnvironmentConfig(config: UIEnvironmentConfig): UIEnvironmentConfig {
  return JSON.parse(JSON.stringify(config)) as UIEnvironmentConfig;
}

// compactClaudeDraft drops unset fields; an absent key means the default.
export function compactClaudeDraft(
  merged: UIEnvironmentConfig['claude'],
): UIEnvironmentConfig['claude'] {
  const next: UIEnvironmentConfig['claude'] = {};
  if (merged.useMantle !== undefined) next.useMantle = merged.useMantle;
  if (merged.useBedrock !== undefined) next.useBedrock = merged.useBedrock;
  // Go JSON order (uiClaudeConfig): the dirty check compares serialized drafts.
  if (merged.useGateway !== undefined) next.useGateway = merged.useGateway;
  if (merged.models !== undefined && merged.models.length > 0) next.models = merged.models;
  if (merged.maxOutputTokens !== undefined) next.maxOutputTokens = merged.maxOutputTokens;
  if (merged.effort !== undefined) next.effort = merged.effort;
  if (merged.defaultModel !== undefined) next.defaultModel = merged.defaultModel;
  if (merged.verboseDebug !== undefined) next.verboseDebug = merged.verboseDebug;
  return next;
}

// aiSessionLaunchSignature is the backend-resolved AI launch command; a change
// reopens the env's AI tabs. Read from the backend so the rules are not copied.
export function aiSessionLaunchSignature(config: UIEnvironmentConfig): string {
  return config.aiLaunchSignature;
}

// claudeRoutingChanged: routing only changes on redeploy, so such a save leaves
// the AI tab reopen to the redeploy.
export function claudeRoutingChanged(
  prior: UIEnvironmentConfig,
  saved: UIEnvironmentConfig,
): boolean {
  const routing = (config: UIEnvironmentConfig) =>
    JSON.stringify([config.claude.useGateway, config.claude.useMantle, config.claude.useBedrock]);
  return routing(prior) !== routing(saved);
}

// versionSourceSignature covers what decides which registries the version picker
// lists from; a change re-queries it.
export function versionSourceSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    registries: config.containerRegistries
      .map((entry) => entry.registry.trim())
      .filter((registry) => registry !== ''),
    localRepoPath: config.localRepoPath,
  });
}

// nextPendingRedeploy stays raised once up; without a prior config it assumes a
// redeploy is needed.
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

// deployRelevantSignature covers only fields a redeploy applies; launch-only and
// desktop-side fields stay out.
function deployRelevantSignature(config: UIEnvironmentConfig): string {
  return JSON.stringify({
    localRepoPath: config.localRepoPath,
    containerRegistries: config.containerRegistries,
    disableBuildScript: config.disableBuildScript,
    runtimeChart: config.runtimeChart,
    platformAccount: config.platformAccount,
    mountSource: config.mountSource,
    repoURL: config.repoURL,
    cloudProviderAlias: config.cloudProviderAlias,
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
  // A tab absent from the table is read-only.
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
    // The rest of the SSH section is applied by its own actions, not Save.
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

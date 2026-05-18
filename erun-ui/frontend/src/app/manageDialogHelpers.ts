import type { ManageTab, UIEnvironmentConfig } from '@/types';

export function cloneEnvironmentConfig(config: UIEnvironmentConfig): UIEnvironmentConfig {
  return JSON.parse(JSON.stringify(config)) as UIEnvironmentConfig;
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
      return compare('containerRegistry', 'cloudProviderAlias', 'snapshot');
    case 'runtime':
      return compare('runtimePod', 'idle');
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
    case 'delete':
      return false;
  }
}

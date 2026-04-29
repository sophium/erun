import type { EnvironmentDialogState } from './state';
import { rememberPastContainerRegistry, rememberPastEnvironment, rememberPastTenant } from './storage';
import { normalizeDialogValue } from './versionSuggestions';
import type { NormalizedEnvironmentDialogValues } from './model';
import type { UISelection } from '@/types';

export function normalizedEnvironmentDialogValues(dialog: EnvironmentDialogState): NormalizedEnvironmentDialogValues {
  return {
    tenant: normalizeDialogValue(dialog.tenant),
    environment: normalizeDialogValue(dialog.environment),
    version: normalizeDialogValue(dialog.version),
    kubernetesContext: normalizeDialogValue(dialog.kubernetesContext),
    containerRegistry: normalizeDialogValue(dialog.containerRegistry),
  };
}

export function validEnvironmentDialogValues(values: NormalizedEnvironmentDialogValues, actionMode: EnvironmentDialogState['actionMode']): boolean {
  if (!values.tenant || !values.environment) {
    return false;
  }
  if (actionMode === 'deploy') {
    return Boolean(values.version);
  }
  return Boolean(values.kubernetesContext && values.containerRegistry);
}

export function rememberEnvironmentDialogSelection(selection: UISelection, actionMode: EnvironmentDialogState['actionMode']): void {
  rememberPastTenant(selection.tenant);
  rememberPastEnvironment(selection.environment);
  if (actionMode === 'init' && selection.containerRegistry) {
    rememberPastContainerRegistry(selection.containerRegistry);
  }
}

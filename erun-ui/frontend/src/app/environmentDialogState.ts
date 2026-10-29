import type { UISelection } from '@/types';

import { environmentTypeIsHost } from './environmentType';
import type { NormalizedEnvironmentDialogValues } from './model';
import { runtimeResourceLimitMessage } from './runtimeResources';
import type { EnvironmentDialogState } from './state';
import {
  rememberPastContainerRegistry,
  rememberPastEnvironment,
  rememberPastTenant,
} from './storage';
import { normalizeDialogValue } from './versionSuggestions';

// environmentDialogResourceLimitMessage exempts host envs, which have no pod
// and no field to satisfy it. Shared by the submit gate and submit.
export function environmentDialogResourceLimitMessage(dialog: EnvironmentDialogState): string {
  if (environmentTypeIsHost(dialog.envType)) {
    return '';
  }
  return runtimeResourceLimitMessage(dialog.runtimePod, dialog.resourceStatus);
}

export function normalizedEnvironmentDialogValues(
  dialog: EnvironmentDialogState,
): NormalizedEnvironmentDialogValues {
  return {
    tenant: normalizeDialogValue(dialog.tenant),
    environment: normalizeDialogValue(dialog.environment),
    version: normalizeDialogValue(dialog.version),
    kubernetesContext: normalizeDialogValue(dialog.kubernetesContext),
    containerRegistry: normalizeDialogValue(dialog.containerRegistry),
    envType: dialog.envType,
    localRepoPath: normalizeDialogValue(dialog.localRepoPath),
  };
}

// missingRequiredFieldReason is the single source for both the Create button
// state and submit, so the button never looks active when submit would fail.
export function missingRequiredFieldReason(dialog: EnvironmentDialogState): string | null {
  const values = normalizedEnvironmentDialogValues(dialog);
  if (!values.tenant || !values.environment) {
    return 'Enter a tenant and environment name.';
  }
  // Mirrors erun-common's ValidateTenantName.
  if (!/^[a-z0-9]{1,63}$/.test(values.tenant)) {
    return 'Tenant name must use only lowercase letters and digits (no hyphens or uppercase).';
  }
  if (values.envType === 'local-agent' || values.envType === 'host') {
    if (!values.localRepoPath) {
      return 'Local repo path is required — this environment is a directory on this machine.';
    }
    // Host envs render neither field, so requiring them would be a dead end.
    if (values.envType === 'host') {
      return null;
    }
  }
  if (!values.kubernetesContext) {
    return 'Select a Kubernetes context.';
  }
  return registryMissingReason(dialog, values.containerRegistry);
}

// Submit checks hosted-registry availability itself rather than trusting the
// disabled control. In-cluster and hosted registries need no host string.
function registryMissingReason(
  dialog: EnvironmentDialogState,
  containerRegistry: string,
): string | null {
  if (dialog.useErunRegistry) {
    return dialog.hostedRegistry?.available === true
      ? null
      : "erun's hosted registry is not available right now. Choose a different registry.";
  }
  if (dialog.useClusterRegistry) {
    return null;
  }
  return containerRegistry ? null : 'Select a container registry.';
}

export function rememberEnvironmentDialogSelection(selection: UISelection): void {
  rememberPastTenant(selection.tenant);
  rememberPastEnvironment(selection.environment);
  if (selection.containerRegistry) {
    rememberPastContainerRegistry(selection.containerRegistry);
  }
}

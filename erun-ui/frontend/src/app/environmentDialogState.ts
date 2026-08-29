import type { UISelection } from '@/types';

import type { NormalizedEnvironmentDialogValues } from './model';
import type { EnvironmentDialogState } from './state';
import {
  rememberPastContainerRegistry,
  rememberPastEnvironment,
  rememberPastTenant,
} from './storage';
import { normalizeDialogValue } from './versionSuggestions';

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

// missingRequiredFieldReason is the single source of truth for what the New
// Environment dialog requires before it can be submitted: it returns a
// user-facing reason for the first missing value, or null when the dialog is
// submittable. Both the Create button's enabled state and submitEnvironmentDialog
// derive from it, so the button can never look active while submit would fail —
// the defect where an unselected Kubernetes context left an active-looking button
// that silently did nothing on click, with no error shown.
export function missingRequiredFieldReason(dialog: EnvironmentDialogState): string | null {
  const values = normalizedEnvironmentDialogValues(dialog);
  if (!values.tenant || !values.environment) {
    return 'Enter a tenant and environment name.';
  }
  // Mirror erun-common's ValidateTenantName: a tenant name is a single DNS-safe
  // label of lowercase letters and digits with no hyphens, so the <tenant>-<env>
  // namespace stays unambiguous. Surface it here so Create is blocked with a clear
  // reason instead of failing only once `erun init` runs in the terminal.
  if (!/^[a-z0-9]{1,63}$/.test(values.tenant)) {
    return 'Tenant name must use only lowercase letters and digits (no hyphens or uppercase).';
  }
  // local-agent envs mount a host directory into the agent pod; the path
  // is required and free-text (the user picks where their project lives).
  if (values.envType === 'local-agent' && !values.localRepoPath) {
    return 'Local repo path is required for local-agent envs.';
  }
  if (!values.kubernetesContext) {
    return 'Select a Kubernetes context.';
  }
  return registryMissingReason(dialog, values.containerRegistry);
}

// registryMissingReason covers all three registry choices: the hosted
// registry is disabled in the UI until its reachability probe resolves
// available, but submit is blocked on that same condition rather than relying
// on the control's disabled state alone. The in-cluster and hosted registries
// need no host string — the first resolves from the kube-context, the second
// from the tenant — so a container registry is only required otherwise.
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

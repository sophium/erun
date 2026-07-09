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
  // local-agent envs mount a host directory into the agent pod; the path
  // is required and free-text (the user picks where their project lives).
  if (values.envType === 'local-agent' && !values.localRepoPath) {
    return 'Local repo path is required for local-agent envs.';
  }
  if (!values.kubernetesContext) {
    return 'Select a Kubernetes context.';
  }
  if (!values.containerRegistry) {
    return 'Select a container registry.';
  }
  return null;
}

export function rememberEnvironmentDialogSelection(selection: UISelection): void {
  rememberPastTenant(selection.tenant);
  rememberPastEnvironment(selection.environment);
  if (selection.containerRegistry) {
    rememberPastContainerRegistry(selection.containerRegistry);
  }
}

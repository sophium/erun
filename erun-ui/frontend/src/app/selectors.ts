import type { UISelection } from '@/types';

import type { RootState } from './store';
import { findVersionSuggestion, normalizeDialogValue, selectionKey } from './versionSuggestions';

export const selectEnvironmentExists = (
  state: RootState,
  tenant: string,
  environment: string,
): boolean =>
  Boolean(
    state.tenants.tenants
      .find((entry) => entry.name === tenant)
      ?.environments.some((env) => env.name === environment),
  );

export const selectSelectedIsPendingFor = (
  state: RootState,
  tenant: string,
  environment: string,
): boolean => {
  const selected = state.selection.selected;
  if (selected?.tenant !== tenant || selected.environment !== environment) {
    return false;
  }
  return !selectEnvironmentExists(state, tenant, environment);
};

// selectPendingOpenAfterDeploy returns the env queued to open once its
// create-time deploy lands — the create→deploy→open gate — or null.
export const selectPendingOpenAfterDeploy = (state: RootState): UISelection | null =>
  state.selection.pendingOpenAfterDeploy;

// selectEnvHasFailedDeploy lets tab respawn and auto-reconnect refuse an env
// whose deploy just failed: reopening a dead tab re-runs open and re-deploys,
// which would re-fail in a loop and storm parallel re-deploys across tabs.
// Recovery is left to the explicit failed-deploy card actions.
export const selectEnvHasFailedDeploy = (
  state: RootState,
  tenant: string,
  environment: string,
): boolean =>
  state.activity.entries.some(
    (entry) =>
      entry.command === 'deploy' &&
      entry.status === 'failed' &&
      entry.tenant === tenant &&
      entry.environment === environment,
  );

export const selectActiveSlotForSelection = (state: RootState, selection: UISelection): number => {
  const tabs = state.terminal.tabsByEnv[selectionKey(selection)] ?? [];
  const first = tabs[0];
  if (!first) {
    return 0;
  }
  const active = tabs.find((tab) => tab.sessionId === state.terminal.sessionId);
  return (active ?? first).slot;
};

// selectManageRuntimeImage resolves the runtime image for the version being
// deployed. It resolves against the dialog-owned suggestion list — the same one
// the picker renders (dialog.versionSuggestions), NOT the shared tenants slice,
// which is written for the sidebar-selected env and clobbered by env-change
// deltas, so the picked version is often absent from it. It must come from a
// real suggestion for that version, not the stored versionImage on its own: a
// stale/mismatched versionImage (or an absent one) would otherwise drop the
// --runtime-image flag, silently deploying the local umbrella's pinned
// erun-devops version instead of the version the operator targeted. Prefer the
// exact (version, image) suggestion so lines that share a version stay distinct,
// then fall back to the first suggestion for the version.
export const selectManageRuntimeImage = (state: RootState, version: string): string => {
  const suggestions = state.manageDialog.versionSuggestions;
  const suggestion =
    findVersionSuggestion(suggestions, version, state.manageDialog.versionImage) ??
    findVersionSuggestion(suggestions, version, '');
  return suggestion?.image ?? '';
};

// selectDialogKubernetesContext picks the env-dialog k8s context to display.
export const selectDialogKubernetesContext = (state: RootState, contexts: string[]): string => {
  const current = normalizeDialogValue(state.environmentDialog.kubernetesContext);
  if (current && contexts.includes(current)) {
    return current;
  }
  return contexts[0] ?? '';
};

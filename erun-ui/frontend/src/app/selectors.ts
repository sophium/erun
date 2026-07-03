import type { UISelection } from '@/types';

import type { RootState } from './store';
import { normalizeDialogValue, selectionKey } from './versionSuggestions';

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

export const selectManageRuntimeImage = (state: RootState, version: string): string => {
  if (state.manageDialog.versionImage) {
    return state.manageDialog.versionImage;
  }
  const suggestion = state.tenants.versionSuggestions.find((value) => value.version === version);
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

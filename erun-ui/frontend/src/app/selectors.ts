import type { UISelection } from '@/types';

import type { RootState } from './store';
import { normalizeDialogValue, selectionKey } from './versionSuggestions';

// Pure selectors derived from store state. These used to live on the
// TerminalController as instance methods even though they touched no
// imperative state. Selectors keep them composable and testable.

export const selectActiveSessionDebug = (state: RootState, sessionId: number): boolean =>
  sessionId > 0 && state.sessions.debugModes[sessionId] !== undefined;

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
  return suggestion?.image || '';
};

// selectDialogKubernetesContext picks the env-dialog k8s context to display.
// Used by the boot/refresh thunks that update the dialog after a list refresh.
export const selectDialogKubernetesContext = (state: RootState, contexts: string[]): string => {
  const current = normalizeDialogValue(state.environmentDialog.kubernetesContext);
  if (current && contexts.includes(current)) {
    return current;
  }
  return contexts[0] || '';
};

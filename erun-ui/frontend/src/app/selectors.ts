import { createSelector } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

import type { SidebarFocus, WhipDefaultTarget } from './model';
import { type NotificationCounts, unreadNotificationCounts } from './notificationCenter';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
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

// selectEnvironmentType decides whether `erun init` already deployed the
// runtime (remote) or the desktop must deploy it (local-agent).
export const selectEnvironmentType = (
  state: RootState,
  tenant: string,
  environment: string,
): string | undefined =>
  state.tenants.tenants
    .find((entry) => entry.name === tenant)
    ?.environments.find((env) => env.name === environment)?.type;

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

export const selectPendingOpenAfterDeploy = (state: RootState): UISelection | null =>
  state.selection.pendingOpenAfterDeploy;

// selectEnvHasFailedDeploy stops respawn and auto-reconnect from re-deploying a
// failed env in a loop.
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

export const selectActiveTabIsAI = (state: RootState): boolean => {
  const selection = state.selection.selected;
  if (!selection) {
    return false;
  }
  const tabs = state.terminal.tabsByEnv[selectionKey(selection)] ?? [];
  const active = tabs.find((tab) => tab.sessionId === state.terminal.sessionId);
  return active?.kind === 'ai';
};

// selectManageRuntimeImage resolves from the dialog's own suggestions, not the
// tenants slice, which tracks the sidebar env; a missing image would drop --runtime-image.
export const selectManageRuntimeImage = (state: RootState, version: string): string => {
  const suggestions = state.manageDialog.versionSuggestions;
  const suggestion =
    findVersionSuggestion(suggestions, version, state.manageDialog.versionImage) ??
    findVersionSuggestion(suggestions, version, '');
  return suggestion?.image ?? '';
};

// selectDialogKubernetesContext keeps a still-valid selection but never
// auto-picks one, so capacity stays hidden until the user chooses.
export const selectDialogKubernetesContext = (state: RootState, contexts: string[]): string => {
  const current = normalizeDialogValue(state.environmentDialog.kubernetesContext);
  if (current && contexts.includes(current)) {
    return current;
  }
  return '';
};

// selectActiveSessionOrchestrator returns the active session's orchestrator;
// use it rather than state.selection.selected, which is the sidebar's selection.
export const selectActiveSessionOrchestrator = (state: RootState): OrchestratorInfo | null => {
  const activeId = state.terminal.sessionId;
  if (activeId <= 0) {
    return null;
  }
  return state.orchestrators.items.find((item) => item.sessionId === activeId) ?? null;
};

export const selectIsOrchestratorSession = (state: RootState): boolean =>
  selectActiveSessionOrchestrator(state) !== null;

// selectSidebarFocus is the single highlighted item: tenant dashboard, then
// orchestrator session, then environment selection.
export const selectSidebarFocus = (state: RootState): SidebarFocus => {
  const dashboardTenant = state.tenantDashboard.tenant;
  if (dashboardTenant) {
    return { kind: 'dashboard', tenant: dashboardTenant };
  }
  const orchestrator = selectActiveSessionOrchestrator(state);
  if (orchestrator) {
    return { kind: 'orchestrator', sessionId: orchestrator.sessionId };
  }
  const selected = state.selection.selected;
  if (selected) {
    return { kind: 'environment', tenant: selected.tenant, environment: selected.environment };
  }
  return { kind: 'none' };
};

// selectWhipDefaultTarget follows selectSidebarFocus's precedence; with nothing
// focused it is null, never "everything".
export const selectWhipDefaultTarget = (state: RootState): WhipDefaultTarget => {
  const orchestrator = selectActiveSessionOrchestrator(state);
  if (orchestrator) {
    return { kind: 'orchestrator', id: orchestrator.id, name: orchestrator.name };
  }
  if (state.tenantDashboard.tenant) {
    return null;
  }
  const selected = state.selection.selected;
  if (selected) {
    const id = `${selected.tenant}/${selected.environment}`;
    return { kind: 'environment', id, name: id };
  }
  return null;
};

export interface ReviewEnvTarget {
  kind: 'env';
  envKey: string;
  tenant: string;
  environment: string;
}

// ReviewDirectoryTarget is a directory an orchestrator works in directly; its
// diff is read with host git.
export interface ReviewDirectoryTarget {
  kind: 'directory';
  // envKey must be stable and never collide with "<tenant>/<environment>".
  envKey: string;
  directory: string;
}

export type ReviewTarget = ReviewEnvTarget | ReviewDirectoryTarget;

export function reviewDirectoryKey(directory: string): string {
  return 'directory:' + directory;
}

export type DiagnosticsContext =
  | { kind: 'orchestrator'; orchestrator: OrchestratorInfo }
  | { kind: 'environment'; tenant: string; environment: string }
  | { kind: 'app' };

// selectDiagnosticsContext uses selectReviewTargets' precedence (orchestrator
// session over sidebar selection).
export const selectDiagnosticsContext = (state: RootState): DiagnosticsContext => {
  const orchestrator = selectActiveSessionOrchestrator(state);
  if (orchestrator) {
    return { kind: 'orchestrator', orchestrator };
  }
  const selected = state.selection.selected;
  if (selected) {
    return { kind: 'environment', tenant: selected.tenant, environment: selected.environment };
  }
  return { kind: 'app' };
};

// selectReviewTargets is an orchestrator session's linked environments and
// directories, else the selected environment.
export const selectReviewTargets = (state: RootState): ReviewTarget[] => {
  const orchestrator = selectActiveSessionOrchestrator(state);
  if (orchestrator) {
    return [
      ...orchestrator.environments.map(
        (env): ReviewTarget => ({
          kind: 'env',
          envKey: `${env.tenant}/${env.environment}`,
          tenant: env.tenant,
          environment: env.environment,
        }),
      ),
      ...orchestrator.directories.map(
        (directory): ReviewTarget => ({
          kind: 'directory',
          envKey: reviewDirectoryKey(directory),
          directory,
        }),
      ),
    ];
  }
  const selection = state.selection.selected;
  if (!selection) {
    return [];
  }
  return [
    {
      kind: 'env',
      envKey: `${selection.tenant}/${selection.environment}`,
      tenant: selection.tenant,
      environment: selection.environment,
    },
  ];
};

// selectReviewTargetBranches is memoized because the dialog re-reads it on every store change.
export const selectReviewTargetBranches = createSelector(
  [
    (state: RootState) => state.tenantDashboard.data?.reviews,
    (state: RootState) => state.tenantDashboard.data?.mergeQueue,
  ],
  (reviews, mergeQueue): string[] => {
    const branches = new Set<string>();
    for (const review of [...(reviews ?? []), ...(mergeQueue ?? [])]) {
      const branch = review.targetBranch.trim();
      if (branch) {
        branches.add(branch);
      }
    }
    return [...branches].sort((left, right) => left.localeCompare(right));
  },
);

export const selectNotificationUnreadCounts = createSelector(
  [(state: RootState) => state.notification.notifications],
  (notifications): NotificationCounts => unreadNotificationCounts(notifications),
);

// selectNotificationHistoryCount keeps the message history reachable once every class is read.
export const selectNotificationHistoryCount = (state: RootState): number =>
  state.notification.notifications.length;

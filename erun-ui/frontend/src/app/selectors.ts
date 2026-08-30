import { createSelector } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

import type { SidebarFocus, WhipDefaultTarget } from './model';
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

// selectEnvironmentType returns the env's declared type (local-agent /
// remote-agent / runtime), or undefined if the env is not in state. The create
// flow uses it to decide whether `erun init` already deployed the runtime
// (remote-worktree envs) or the desktop must still compose the deploy
// (local-agent, which init does not deploy).
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

// selectActiveTabIsAI reports whether the terminal pane's currently-selected
// tab (for the currently-selected environment) is the AI tab — used to gate
// the "another agent is already here" persistent indicator to the one tab it
// actually applies to.
export const selectActiveTabIsAI = (state: RootState): boolean => {
  const selection = state.selection.selected;
  if (!selection) {
    return false;
  }
  const tabs = state.terminal.tabsByEnv[selectionKey(selection)] ?? [];
  const active = tabs.find((tab) => tab.sessionId === state.terminal.sessionId);
  return active?.kind === 'ai';
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

// selectDialogKubernetesContext resolves the env-dialog's k8s context across a
// context-list (re)load: it PRESERVES a still-valid prior selection, but must NOT
// auto-pick a context the user never chose. Auto-picking contexts[0] made the
// dialog fetch and display a capacity reading for a cluster the
// user hadn't selected — while the submit gate still (correctly) required one,
// so the panel showed capacity with the context dropdown on its placeholder.
// Return empty when there is no valid current selection so capacity stays hidden
// until the user picks a context (and the fetch is skipped entirely).
export const selectDialogKubernetesContext = (state: RootState, contexts: string[]): string => {
  const current = normalizeDialogValue(state.environmentDialog.kubernetesContext);
  if (current && contexts.includes(current)) {
    return current;
  }
  return '';
};

// selectActiveSessionOrchestrator returns the orchestrator whose terminal
// session is currently active, or null when the active session is an
// environment tab.
//
// This derivation used to live inline in TerminalTabStrip, which was the only
// component that had it. The titlebar never got it, so its right-hand cluster
// went on acting on state.selection.selected -- the SIDEBAR's environment
// selection, which is independent of which terminal tab is active. With an
// orchestrator tab focused, "Open in VS Code" opened an IDE against an
// environment the orchestrator may not even be linked to (#1178).
//
// It returns the orchestrator rather than a boolean because a cross-env surface
// needs its `environments` list, not just the knowledge that one is active.
export const selectActiveSessionOrchestrator = (state: RootState): OrchestratorInfo | null => {
  const activeId = state.terminal.sessionId;
  if (activeId <= 0) {
    return null;
  }
  return state.orchestrators.items.find((item) => item.sessionId === activeId) ?? null;
};

// selectIsOrchestratorSession is the boolean form, for a caller that only needs
// to know whether env-scoped chrome applies.
export const selectIsOrchestratorSession = (state: RootState): boolean =>
  selectActiveSessionOrchestrator(state) !== null;

// selectSidebarFocus names the one thing the sidebar highlights and the main
// pane shows. The tenant dashboard, an orchestrator's session, and an
// environment's session each used to compute their own "active" condition
// from a different state slice, so a tenant dashboard row and an orchestrator
// row could both render selected at once while the pane showed only one of
// them (#1204). Every sidebar row derives its highlight from this single
// value instead. The tenant dashboard takes priority regardless of a stale
// terminal.sessionId left over from before it opened; an orchestrator session
// takes priority over an environment selection for the same reason.
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

// selectWhipDefaultTarget resolves the one target Whip preselects: the same
// orchestrator-session-over-sidebar-selection precedence selectSidebarFocus
// already established, translated into the id/name a whip target row uses.
// The tenant dashboard and "nothing focused" both resolve to null rather than
// falling back to any environment or orchestrator -- an unfocused whip must
// have no default, never "everything" (erun#1700).
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
  envKey: string;
  tenant: string;
  environment: string;
}

// DiagnosticsContext names which evidence the Diagnostics console shows: an
// orchestrator's own state, the selected environment's, or — when neither is
// active — the desktop app itself, so the panel is never blank.
export type DiagnosticsContext =
  | { kind: 'orchestrator'; orchestrator: OrchestratorInfo }
  | { kind: 'environment'; tenant: string; environment: string }
  | { kind: 'app' };

// selectDiagnosticsContext mirrors selectReviewEnvTargets' own precedence
// (orchestrator session over sidebar selection) rather than introducing a
// second notion of "what's active" — an orchestrator session used to leave
// the Diagnostics panel reading "environment: none selected" with no trace,
// because it derived its context from state.selection.selected alone (#1241).
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

// selectReviewEnvTargets resolves which environments the diff panel shows: an
// orchestrator session's linked environments in its configured order, else the
// single selected environment. A single environment is the one-entry case, so
// the panel has one code path rather than two (#1178).
export const selectReviewEnvTargets = (state: RootState): ReviewEnvTarget[] => {
  const orchestrator = selectActiveSessionOrchestrator(state);
  if (orchestrator) {
    return orchestrator.environments.map((env) => ({
      envKey: `${env.tenant}/${env.environment}`,
      tenant: env.tenant,
      environment: env.environment,
    }));
  }
  const selection = state.selection.selected;
  if (!selection) {
    return [];
  }
  return [
    {
      envKey: `${selection.tenant}/${selection.environment}`,
      tenant: selection.tenant,
      environment: selection.environment,
    },
  ];
};

// selectReviewTargetBranches lists the branches this tenant's reviews and
// merge queue already target, so opening a review offers the branches that
// exist rather than asking the operator to retype one from memory. Memoized:
// the dialog re-reads it on every store change.
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

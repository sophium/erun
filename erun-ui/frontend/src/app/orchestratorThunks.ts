import {
  CreateOrchestrator,
  DeleteOrchestrator,
  InvestigateFailure,
  ListOrchestrators,
  ReportBugFailure,
  ResolveOrchestratorToReopen,
  RestartApp,
  RestartOrchestrator,
  RevealOrchestratorGuidance,
  StartOrchestrator,
  StopOrchestrator,
  UpdateOrchestrator,
} from '../../wailsjs/go/main/App';
import { BrowserOpenURL, ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { stateApi } from './api/stateApi';
import { buildDiagnosticsIssueURL, diagnosticsIssueBody } from './diagnosticsIssue';
import { readError } from './errors';
import type { IDEKind, OrchestratorGuidanceLayer } from './model';
import { showNotification } from './notificationThunks';
import { planOrchestratorBusySeed } from './orchestratorBusySeed';
import { planOrchestratorRestore } from './orchestratorRestore';
import { planOrchestratorShellSeed } from './orchestratorShellActivitySeed';
import { setAIBusyForSession } from './slices/aiActivitySlice';
import { setShellActivityForSession } from './slices/orchestratorShellActivitySlice';
import {
  closeOrchestratorDialog,
  type OrchestratorAlias,
  type OrchestratorEnvRef,
  type OrchestratorInfo,
  type OrchestratorLaunchSettings,
  setOrchestrators,
  setOrchestratorsBusy,
  setOrchestratorsError,
} from './slices/orchestratorsSlice';
import { setSelected } from './slices/selectionSlice';
import { resetTenantDashboard } from './slices/tenantDashboardSlice';
import { setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';

// focusOrchestratorSession makes an orchestrator's session own the terminal
// pane. The pane and the sidebar highlight are each derived from whichever of
// the tenant dashboard, an orchestrator's session, or an environment
// selection currently applies (see selectSidebarFocus in ./selectors), so
// handing an orchestrator the pane must clear the other two here, once,
// rather than at every call site that starts, opens, or restarts one — a
// caller that dispatches setSessionId directly leaves the tenant dashboard
// painted over the session the sidebar highlights as focused.
const focusOrchestratorSession =
  (sessionId: number): AppThunk =>
  (dispatch) => {
    dispatch(resetTenantDashboard());
    dispatch(setSelected(null));
    dispatch(setSessionId(sessionId));
  };

// loadOrchestrators fetches the current list and, in the same pass, seeds the
// AI-busy store from each orchestrator's own `busy` snapshot field,
// and the shell-activity store from its shellRunning/shellCommand/
// shellStartedAtUnix fields — the same store fields the ai-activity and
// orchestrator-shell-activity events write to, so a fetch that lands after a
// transition (boot, a dialog close-and-reload, a manual refresh) renders the
// true state even if that event was never observed.
export const loadOrchestrators = (): AppThunk<Promise<void>> => async (dispatch) => {
  try {
    const list = (await ListOrchestrators()) as OrchestratorInfo[] | null;
    const items = list ?? [];
    dispatch(setOrchestrators(items));
    for (const seed of planOrchestratorBusySeed(items)) {
      dispatch(setAIBusyForSession(seed));
    }
    for (const seed of planOrchestratorShellSeed(items)) {
      dispatch(
        setShellActivityForSession({
          sessionId: seed.sessionId,
          activity: {
            running: seed.running,
            command: seed.command,
            startedAtUnix: seed.startedAtUnix,
          },
        }),
      );
    }
  } catch (error) {
    dispatch(setOrchestratorsError(readError(error)));
  }
};

// createOrchestrator persists a new orchestrator linking the chosen agent
// environments (each with the host directory it is reviewed in) plus the
// directories of its own, the platform alias it declares as its own and its
// launch settings, and closes the dialog.
export const createOrchestrator =
  (
    name: string,
    envs: OrchestratorEnvRef[],
    directories: string[],
    alias: OrchestratorAlias,
    launch: OrchestratorLaunchSettings,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await CreateOrchestrator(name, envs, directories, alias, launch);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// updateOrchestrator edits an existing orchestrator's linked environments, its
// own directories, its name, the platform alias it declares as its own, and
// its launch settings. Every field is sent, not only the changed one: the
// backend replaces the whole definition, so an omitted field is erased rather
// than left alone.
export const updateOrchestrator =
  (
    id: string,
    name: string,
    envs: OrchestratorEnvRef[],
    directories: string[],
    alias: OrchestratorAlias,
    launch: OrchestratorLaunchSettings,
  ): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await UpdateOrchestrator(id, name, envs, directories, alias, launch);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// startOrchestrator spawns the session for a definition and makes it the active
// terminal so the operator lands in it immediately.
export const startOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await StartOrchestrator(id, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// openOrchestrator attaches the terminal pane to an already-running orchestrator.
export const openOrchestrator =
  (sessionId: number): AppThunk =>
  (dispatch) => {
    if (sessionId > 0) {
      dispatch(focusOrchestratorSession(sessionId));
    }
  };

// restartOrchestrator tears down a running orchestrator's session and spawns a
// fresh one — which resumes that orchestrator's own pinned conversation — then
// re-focuses the new session (a fresh serial; reusing the old one would attach to
// a dead PTY).
export const restartOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await RestartOrchestrator(id, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// restartApp relaunches the desktop app and asks it to give the given
// orchestrator the terminal pane on the way back up (empty id just restarts).
// Orchestrators run as background agents, so the restart does not interrupt
// them: the new desktop reattaches each one.
export const restartApp =
  (returnToOrchestratorId: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await RestartApp(returnToOrchestratorId);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// restoreOpenOrchestrators reopens every orchestrator this launch should come
// back to — everything that was open when the desktop last ran — and returns
// whether one of them ended up owning the terminal pane, so the caller (boot)
// can skip the default environment selection. The pane is single but the
// restored set is not: exactly one orchestrator (the restart hand-off's target,
// or the most recently (re)started one) owns it and clears the env selection —
// otherwise boot's default selection and the selection-sync middleware would
// reconcile the terminal back to an environment — while every other
// orchestrator in the set is reopened too, off the pane, so the tab strip and
// the live sessions agree instead of a tab surviving with no session behind it.
// Each one reattaches to its own background agent, on whatever conversation
// that agent is on.
export const restoreOpenOrchestrators =
  (): AppThunk<Promise<boolean>> => async (dispatch, getState) => {
    try {
      const target = await ResolveOrchestratorToReopen();
      const { primary, alsoReopen } = planOrchestratorRestore(
        target,
        getState().orchestrators.items,
      );
      if (!primary && alsoReopen.length === 0) {
        return false;
      }

      for (const id of alsoReopen) {
        try {
          await StartOrchestrator(id, 80, 24);
        } catch (error) {
          dispatch(setOrchestratorsError(readError(error)));
        }
      }

      let restoredPane = false;
      if (primary) {
        const info = await StartOrchestrator(primary, 80, 24);
        dispatch(focusOrchestratorSession(info.sessionId));
        restoredPane = true;
      }
      await dispatch(loadOrchestrators());
      return restoredPane;
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
      return false;
    }
  };

export const stopOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await StopOrchestrator(id);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
    await dispatch(loadOrchestrators());
  };

// deleteOrchestrator removes a persisted definition — the linked environments'
// workspace-sync config is left intact — and closes the dialog on success. On
// failure it surfaces the error and keeps the dialog open so the operator can
// retry, matching create/update.
export const deleteOrchestrator =
  (id: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await DeleteOrchestrator(id);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// revealOrchestratorGuidance opens one of an orchestrator's two guidance
// layers — its own standing role or the shared contract every orchestrator
// obeys — in the operator's chosen host IDE. Errors surface the same way
// create/update/delete already do in OrchestratorDialog: inline, via the
// shared `error` state, rather than a second notification channel.
export const revealOrchestratorGuidance =
  (id: string, layer: OrchestratorGuidanceLayer, ide: IDEKind): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await RevealOrchestratorGuidance(id, layer, ide);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// investigateFailure spawns a transient orchestrator, grouped under the failed
// env's tenant, seeded to fix the failure, then makes it the active terminal.
export const investigateFailure =
  (report: string, tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await InvestigateFailure(report, tenant, environment, 80, 24);
      dispatch(focusOrchestratorSession(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// reportFailure hands a failure to an agent that drafts a GitHub issue for it
// (root AGENTS.md "Smooth, Seamless, No Dead Ends" — "Report a bug" is a
// standing action, not a form the operator fills in) rather than widening the
// existing prefilled-URL handoff. Reuses investigateFailure's exact spawn
// plumbing and its shared investigation_bounds.go population, so the three
// outcomes ReportBugFailure can return are handled the same way here:
//   - admitted: the draft session is focused, the same "reachable, not modal"
//     feedback investigateFailure already uses instead of a bespoke dialog.
//   - refused, naming an existing draft/investigation for the same failure:
//     that session is focused instead, and an info notice says why — the
//     "repeat click focuses the draft" requirement, not a second agent.
//   - any other refusal (or no agent available): falls back to the existing
//     prefilled-URL report, naming the refusal so the operator knows why the
//     agent path did not apply, never as a silent downgrade.
export const reportFailure =
  (report: string, summary: string, tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    try {
      const outcome = await ReportBugFailure(report, tenant, environment, 80, 24);
      if (outcome.admitted) {
        dispatch(focusOrchestratorSession(outcome.orchestrator.sessionId));
        await dispatch(loadOrchestrators());
        return;
      }
      if (outcome.existingId) {
        await dispatch(loadOrchestrators());
        const existing = getState().orchestrators.items.find(
          (item) => item.id === outcome.existingId,
        );
        if (existing) {
          dispatch(focusOrchestratorSession(existing.sessionId));
        }
        dispatch(
          showNotification(
            'info',
            outcome.message ?? 'Already drafting a report for this failure.',
            { tenant, environment },
          ),
        );
        return;
      }
      dispatch(openFallbackReportURL(report, summary, tenant, environment, outcome.message ?? ''));
    } catch (error) {
      dispatch(openFallbackReportURL(report, summary, tenant, environment, readError(error)));
    }
  };

// openFallbackReportURL is the existing DebugPanel.Report.tsx path (a
// prefilled github.com/sophium/erun issue the operator reviews and submits in
// the browser), reused here as the never-a-dead-end floor: no agent could
// draft this report, but the operator still gets a form pre-filled with the
// evidence rather than an error with nothing behind it.
const openFallbackReportURL =
  (
    report: string,
    summary: string,
    tenant: string,
    environment: string,
    reason: string,
  ): AppThunk =>
  (dispatch, getState) => {
    const build =
      stateApi.endpoints.getInitialState.select(undefined)(getState()).data?.build ?? null;
    const title = tenant && environment ? `${tenant}/${environment}: ${summary}` : summary;
    const body = diagnosticsIssueBody(report, build);
    const { url } = buildDiagnosticsIssueURL(title, body);
    BrowserOpenURL(url);
    void ClipboardSetText(report);
    const prefix = reason ? `${reason} ` : '';
    dispatch(
      showNotification(
        'warning',
        `${prefix}Opened a prefilled issue in the browser instead — the full report is on your clipboard.`,
        { tenant, environment },
      ),
    );
  };

import {
  CreateOrchestrator,
  DeleteOrchestrator,
  InvestigateFailure,
  ListOrchestrators,
  ResolveOrchestratorToReopen,
  RestartApp,
  RestartOrchestrator,
  RevealOrchestratorGuidance,
  StartOrchestrator,
  StartOrchestratorWithResume,
  StopOrchestrator,
  UpdateOrchestrator,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
import type { IDEKind, OrchestratorGuidanceLayer } from './model';
import { planOrchestratorBusySeed } from './orchestratorBusySeed';
import { planOrchestratorRestore, readRestoreNotice } from './orchestratorRestore';
import { planOrchestratorShellSeed } from './orchestratorShellActivitySeed';
import { setAIBusyForSession } from './slices/aiActivitySlice';
import { setShellActivityForSession } from './slices/orchestratorShellActivitySlice';
import {
  closeOrchestratorDialog,
  type OrchestratorEnvRef,
  type OrchestratorInfo,
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
// caller that dispatched setSessionId directly used to leave the tenant
// dashboard painted over the session the sidebar now highlighted as focused
// (#1204).
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
// environments (each with the host directory it is reviewed in) and closes the
// dialog.
export const createOrchestrator =
  (name: string, envs: OrchestratorEnvRef[]): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await CreateOrchestrator(name, envs);
      dispatch(closeOrchestratorDialog());
      await dispatch(loadOrchestrators());
      dispatch(setOrchestratorsBusy(false));
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// updateOrchestrator edits an existing orchestrator's linked environments and name.
export const updateOrchestrator =
  (id: string, name: string, envs: OrchestratorEnvRef[]): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(setOrchestratorsBusy(true));
    try {
      await UpdateOrchestrator(id, name, envs);
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

// restartApp relaunches the desktop app and asks it to reopen the given
// orchestrator on the way back up (empty id just restarts). The backend records
// which conversation is live and hands it back its task on resume, so a restart
// taken to pick up a rebuild continues rather than idling.
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
// back to — everything that was open when the desktop last ran, or a restart's
// hand-off plus everything else that was open alongside it — and returns
// whether one of them ended up owning the terminal pane, so the caller (boot)
// can skip the default environment selection. The pane is single but the
// restored set is not: exactly one orchestrator (the restart hand-off's target,
// or the most recently (re)started one) owns it, resumes its conversation and
// clears the env selection — otherwise boot's default selection and the
// selection-sync middleware would reconcile the terminal back to an
// environment — while every other orchestrator in the set is started too but
// left idle, off the pane, so the tab strip and the live sessions agree instead
// of a tab surviving with no session behind it. Only the pane owner can carry a
// resume prompt, so a plain launch resumes every restored orchestrator idle; a
// restart hand-off's prompt auto-runs for the one orchestrator it named, never
// for the rest of the set. A refused hand-off still reopens its orchestrator
// and surfaces the backend's notice beside the orchestrator list, so a resume
// that declined to continue is never silent. Which conversation each
// orchestrator resumes is the backend's call, not a re-derivation here: this
// thunk just resumes whatever conversationId the target names, or starts
// fresh when none was resolved.
export const restoreOpenOrchestrators =
  (): AppThunk<Promise<boolean>> => async (dispatch, getState) => {
    try {
      const target = await ResolveOrchestratorToReopen();
      const notice = readRestoreNotice(target);
      if (notice) {
        dispatch(setOrchestratorsError(notice));
      }
      const { primary, alsoReopen } = planOrchestratorRestore(
        target,
        getState().orchestrators.items,
      );
      if (!primary && alsoReopen.length === 0) {
        return false;
      }

      for (const ref of alsoReopen) {
        try {
          // A resolved conversation id resumes exactly that conversation; its
          // absence means the backend found nothing safe to resume, so this
          // orchestrator starts fresh instead.
          if (ref.conversationId) {
            await StartOrchestratorWithResume(ref.orchestratorId, ref.conversationId, '', 80, 24);
          } else {
            await StartOrchestrator(ref.orchestratorId, 80, 24);
          }
        } catch (error) {
          dispatch(setOrchestratorsError(readError(error)));
        }
      }

      let restoredPane = false;
      if (primary) {
        // A resolved conversation id resumes exactly that conversation — with
        // the resume prompt handed to it so a rebuild+restart continues on its
        // own instead of idling, or with none for a plain launch that just
        // lands the operator back where they were. Its absence means the
        // backend found nothing safe to resume, so this orchestrator starts
        // fresh instead of a re-derivation that could land on the wrong one.
        const info = primary.conversationId
          ? await StartOrchestratorWithResume(
              primary.id,
              primary.conversationId,
              primary.resumePrompt,
              80,
              24,
            )
          : await StartOrchestrator(primary.id, 80, 24);
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

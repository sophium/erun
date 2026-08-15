import {
  CreateOrchestrator,
  DeleteOrchestrator,
  InvestigateFailure,
  ListOrchestrators,
  ResolveOrchestratorToReopen,
  RestartApp,
  RestartOrchestrator,
  StartOrchestrator,
  StartOrchestratorWithResume,
  StopOrchestrator,
  UpdateOrchestrator,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { planOrchestratorRestore, readRestoreNotice } from './orchestratorRestore';
import {
  closeOrchestratorDialog,
  type OrchestratorEnvRef,
  type OrchestratorInfo,
  setOrchestrators,
  setOrchestratorsBusy,
  setOrchestratorsError,
} from './slices/orchestratorsSlice';
import { setSelected } from './slices/selectionSlice';
import { setSessionId } from './slices/terminalSlice';
import type { AppThunk } from './store';

export const loadOrchestrators = (): AppThunk<Promise<void>> => async (dispatch) => {
  try {
    const list = (await ListOrchestrators()) as OrchestratorInfo[] | null;
    dispatch(setOrchestrators(list ?? []));
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
      dispatch(setSessionId(info.sessionId));
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
      dispatch(setSessionId(sessionId));
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
      dispatch(setSessionId(info.sessionId));
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

// restoreOpenOrchestrator reopens the orchestrator this launch should come back
// to — the one that was open when the desktop last ran, or the one a restart
// handed off — and returns whether it did, so the caller (boot) can skip the
// default environment selection. Only a restart hand-off carries a resume
// prompt, so a plain launch resumes the conversation idle and auto-runs nothing.
// A refused hand-off still reopens the orchestrator and surfaces the backend's
// notice beside the orchestrator list, so a resume that declined to continue is
// never silent. The restored orchestrator OWNS the pane, so the env selection is
// cleared — otherwise boot's default selection and the selection-sync middleware
// reconcile the terminal back to an environment and the operator never lands in
// the resumed session.
export const restoreOpenOrchestrator =
  (): AppThunk<Promise<boolean>> => async (dispatch, getState) => {
    try {
      const target = await ResolveOrchestratorToReopen();
      const notice = readRestoreNotice(target);
      if (notice) {
        dispatch(setOrchestratorsError(notice));
      }
      const plan = planOrchestratorRestore(target, getState().orchestrators.items);
      if (!plan) {
        return false;
      }
      // With a resume prompt, resume the conversation that asked for the restart
      // AND hand it the task so a rebuild+restart continues on its own instead of
      // idling at the prompt; without one, just resume the orchestrator's own
      // pinned conversation.
      const info = plan.resumePrompt
        ? await StartOrchestratorWithResume(plan.id, plan.conversationId, plan.resumePrompt, 80, 24)
        : await StartOrchestrator(plan.id, 80, 24);
      dispatch(setSelected(null));
      dispatch(setSessionId(info.sessionId));
      await dispatch(loadOrchestrators());
      return true;
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

// investigateFailure spawns a transient orchestrator, grouped under the failed
// env's tenant, seeded to fix the failure, then makes it the active terminal.
export const investigateFailure =
  (report: string, tenant: string, environment: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      const info = await InvestigateFailure(report, tenant, environment, 80, 24);
      dispatch(setSessionId(info.sessionId));
      await dispatch(loadOrchestrators());
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

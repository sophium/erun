import {
  ConsumeRelaunchTarget,
  CreateOrchestrator,
  DeleteOrchestrator,
  InvestigateFailure,
  ListOrchestrators,
  RestartApp,
  RestartOrchestrator,
  StartOrchestrator,
  StartOrchestratorWithResume,
  StopOrchestrator,
  UpdateOrchestrator,
} from '../../wailsjs/go/main/App';
import { readError } from './errors';
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

// createOrchestrator persists a new orchestrator linking the chosen remote-agent
// environments (each wired to its host mirror directory) and closes the dialog.
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
// fresh one — its Claude conversation resumes via `--continue` — then re-focuses
// the new session (a fresh serial; reusing the old one would attach to a dead PTY).
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
// orchestrator on the way back up (empty id just restarts). The fresh instance
// resumes that orchestrator's conversation via `--continue`.
export const restartApp =
  (returnToOrchestratorId: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    try {
      await RestartApp(returnToOrchestratorId);
    } catch (error) {
      dispatch(setOrchestratorsError(readError(error)));
    }
  };

// restoreOrchestratorAfterRestart reopens the orchestrator a restart asked to
// return to, if it still exists as a persisted definition, and returns whether
// it did so the caller (boot) can skip the default environment selection.
// One-shot: the backend clears the target on read, so a plain launch never
// auto-starts anything. The restored orchestrator OWNS the pane, so the env
// selection is cleared — otherwise boot's default selection and the
// selection-sync middleware reconcile the terminal back to an environment and
// the operator never lands in the resumed session.
export const restoreOrchestratorAfterRestart =
  (): AppThunk<Promise<boolean>> => async (dispatch, getState) => {
    try {
      const relaunch = await ConsumeRelaunchTarget();
      const id = relaunch.orchestratorId;
      if (!id) {
        return false;
      }
      const target = getState().orchestrators.items.find((o) => o.id === id && !o.transient);
      if (!target) {
        return false;
      }
      // With a resume prompt, resume the conversation AND hand it the task so a
      // rebuild+restart continues on its own instead of idling at the prompt;
      // without one, just resume via --continue.
      const info = relaunch.resumePrompt
        ? await StartOrchestratorWithResume(id, relaunch.resumePrompt, 80, 24)
        : await StartOrchestrator(id, 80, 24);
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

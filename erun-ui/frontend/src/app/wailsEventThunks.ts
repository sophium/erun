import type { TerminalExitPayload, UISelection } from '@/types';

import { reloadStateAfterEnvironmentChange } from './bootThunks';
import { environmentTypeIsHost, environmentTypeIsRemoteWorktree } from './environmentType';
import { readError } from './errors';
import type {
  AIActivityPayload,
  AppNotificationPayload,
  AppStatusPayload,
  DoctorCompletedPayload,
  EnvActivityPayload,
  EnvironmentInitializedPayload,
  EnvNodePayload,
  EnvStatusPayload,
  EnvUsagePayload,
  OrchestratorShellActivityPayload,
  SSHDInitCompletedPayload,
  TerminalExitSelections,
} from './model';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalError,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import {
  selectEnvironmentExists,
  selectEnvironmentType,
  selectPendingOpenAfterDeploy,
  selectSelectedIsPendingFor,
} from './selectors';
import { openSelection, selectTerminalTab, startInitialDeploySelection } from './sessionThunks';
import { setAIBusyForEnv, setAIBusyForSession } from './slices/aiActivitySlice';
import { recordDoctorOutcome } from './slices/doctorSlice';
import {
  setEnvActivityForEnv,
  setEnvNodeForEnv,
  setEnvStatusForEnv,
  setEnvUsageForEnv,
} from './slices/envStatusSlice';
import { setShellActivityForSession } from './slices/orchestratorShellActivitySlice';
import {
  setEnvActivityForOrchestratorEnvs,
  setEnvUsageForOrchestratorEnvs,
} from './slices/orchestratorsSlice';
import { appendReconnectLine } from './slices/reviewSlice';
import {
  clearPendingOpenAfterDeploy,
  setPendingOpenAfterDeploy,
  setSelected,
} from './slices/selectionSlice';
import { envClosing, recordExitOutput, recordExitReason } from './slices/sessionsSlice';
import { recordSSHDInitOutcome } from './slices/sshdInitSlice';
import type { AppDispatch, AppThunk } from './store';
import { releaseExitedSession, removeTab } from './tabsThunks';
import { failedTerminalOutput } from './terminalBuffers';
import {
  classifiedTerminalFailure,
  failedTerminalExitReason,
  statusForTerminalOutput,
  successfulTerminalExitReason,
  terminalExitHasTrackedSelection,
} from './terminalStatus';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// Wails events are handled as thunks, except terminal-output, which stays on
// the controller with the buffers and xterm.

export const handleAIActivity =
  (payload: AIActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      if (payload.sessionId > 0) {
        dispatch(setAIBusyForSession({ sessionId: payload.sessionId, busy: payload.busy }));
      }
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setAIBusyForEnv({ key, busy: payload.busy }));
  };

// handleOrchestratorShellActivity reports a background shell still running
// after the orchestrator's turn went idle.
export const handleOrchestratorShellActivity =
  (payload: OrchestratorShellActivityPayload): AppThunk =>
  (dispatch) => {
    if (payload.sessionId <= 0) {
      return;
    }
    dispatch(
      setShellActivityForSession({
        sessionId: payload.sessionId,
        activity: {
          running: payload.running,
          command: payload.command,
          startedAtUnix: payload.startedAtUnix,
        },
      }),
    );
  };

export const handleEnvStatus =
  (payload: EnvStatusPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setEnvStatusForEnv({ key, status: payload.status.trim() }));
  };

// handleEnvActivity also patches the orchestrator env refs for this environment,
// so cards and sidebar rows agree.
export const handleEnvActivity =
  (payload: EnvActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const activity = {
      reachable: payload.reachable,
      observed: payload.observed,
      outage: payload.outage === true,
      checkFailed: payload.checkFailed === true,
      busy: payload.busy,
      detail: (payload.detail ?? '').trim(),
    };
    const key = selectionKey({ tenant, environment });
    dispatch(setEnvActivityForEnv({ key, activity }));
    dispatch(setEnvActivityForOrchestratorEnvs({ tenant, environment, activity }));
  };

// handleEnvUsage updates the sidebar and orchestrator cards from the same event.
export const handleEnvUsage =
  (payload: EnvUsagePayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const usage = {
      usage: payload.usage,
      observedAtUnix: payload.observedAtUnix,
      staleAfterSeconds: payload.staleAfterSeconds,
    };
    const key = selectionKey({ tenant, environment });
    dispatch(setEnvUsageForEnv({ key, usage }));
    dispatch(setEnvUsageForOrchestratorEnvs({ tenant, environment, usage }));
  };

export const handleEnvNode =
  (payload: EnvNodePayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setEnvNodeForEnv({ key, node: payload.node }));
  };

export const handleAppStatus =
  (payload: AppStatusPayload): AppThunk =>
  (dispatch) => {
    const message = (payload.message ?? '').trim();
    if (!message) {
      return;
    }
    dispatch(showTerminalMessage(message, payload.busy === true));
  };

export const handleAppNotification =
  (payload: AppNotificationPayload): AppThunk =>
  (dispatch) => {
    const message = (payload.message ?? '').trim();
    if (!message) {
      return;
    }
    const kind = payload.kind ?? 'info';
    dispatch(
      showNotification(kind, message, {
        tenant: payload.tenant,
        environment: payload.environment,
        source: payload.source,
        orchestratorId: payload.orchestratorId,
        action: payload.action,
      }),
    );
  };

// handleEventsDropped warns and resyncs, since the dropped events cannot be known.
export const handleEventsDropped =
  (missed: number): AppThunk<Promise<void>> =>
  async (dispatch) => {
    console.error(
      `erun headless: missed ${missed.toString()} event(s) before this marker; resyncing`,
    );
    dispatch(
      showNotification(
        'warning',
        `Lost ${missed.toString()} update${missed === 1 ? '' : 's'} from the app — refreshing to catch up.`,
      ),
    );
    await dispatch(reloadStateAfterEnvironmentChange());
  };

// A single reload can miss a just-initialized env, so retry a bounded number of times.
const ENVIRONMENT_INIT_RELOAD_ATTEMPTS = 8;
const ENVIRONMENT_INIT_RELOAD_DELAY_MS = 400;

const delayMs = (ms: number): Promise<void> =>
  new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });

const reloadUntilEnvironmentVisible =
  (tenant: string, environment: string): AppThunk<Promise<boolean>> =>
  async (dispatch, getState) => {
    for (let attempt = 0; attempt < ENVIRONMENT_INIT_RELOAD_ATTEMPTS; attempt += 1) {
      if (attempt > 0) {
        await delayMs(ENVIRONMENT_INIT_RELOAD_DELAY_MS);
      }
      await dispatch(reloadStateAfterEnvironmentChange());
      if (selectEnvironmentExists(getState(), tenant, environment)) {
        return true;
      }
    }
    return false;
  };

// Remote-worktree envs are deployed by init (a second deploy would roll the
// fresh pod) and host envs need no deploy, so both open directly; local-agent
// envs compose a deploy and open on environment-deployed.
export const handleEnvironmentInitialized =
  (payload: EnvironmentInitializedPayload): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const visible = await dispatch(reloadUntilEnvironmentVisible(tenant, environment));
    if (!visible) {
      dispatch(
        showNotification(
          'error',
          `Created ${tenant} / ${environment}, but it did not appear in the sidebar. Reopen ERun to refresh.`,
        ),
      );
      return;
    }
    dispatch(showNotification('success', `Created ${tenant} / ${environment}.`));
    const envType = selectEnvironmentType(getState(), tenant, environment);
    if (environmentTypeIsHost(envType)) {
      // `erun deploy` refuses a host env; opening is just a shell in its directory.
      try {
        await dispatch(openSelection({ tenant, environment }));
      } catch (error) {
        dispatch(showTerminalError(readError(error)));
      }
      return;
    }
    if (environmentTypeIsRemoteWorktree(envType)) {
      try {
        await dispatch(openSelection({ tenant, environment }));
      } catch (error) {
        dispatch(showTerminalError(readError(error)));
      }
      return;
    }
    dispatch(setPendingOpenAfterDeploy({ tenant, environment }));
    try {
      await dispatch(startInitialDeploySelection({ tenant, environment }));
    } catch (error) {
      dispatch(clearPendingOpenAfterDeploy());
      dispatch(showTerminalError(readError(error)));
    }
  };

// handleEnvironmentDeployed opens tabs only for an env queued pending-open at create.
export const handleEnvironmentDeployed =
  (payload: EnvironmentInitializedPayload): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const pending = selectPendingOpenAfterDeploy(getState());
    if (pending?.tenant !== tenant || pending.environment !== environment) {
      return;
    }
    dispatch(clearPendingOpenAfterDeploy());
    try {
      await dispatch(openSelection({ tenant, environment }));
    } catch (error) {
      dispatch(showTerminalError(readError(error)));
    }
  };

export const handleEnvironmentInitFailed =
  (payload: EnvironmentInitializedPayload): AppThunk =>
  (dispatch, getState) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    dispatch(
      showNotification(
        'error',
        `Failed to create ${tenant} / ${environment}. See the Local tab and the activity drawer for details.`,
      ),
    );
    if (selectSelectedIsPendingFor(getState(), tenant, environment)) {
      dispatch(setSelected(null));
    }
  };

export const handleReconnectLine =
  (line: string): AppThunk =>
  (dispatch, getState) => {
    const trimmed = line.trim();
    if (!trimmed) {
      return;
    }
    if (getState().review.reconnect.status !== 'running') {
      return;
    }
    dispatch(appendReconnectLine(trimmed));
  };

export const updateOpenStatusFromOutput =
  (sessionId: number, output: string): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (!output || state.sessions.openSelections[sessionId] === undefined) {
      return;
    }
    if (state.terminalStatus.terminalCopyOutput) {
      return;
    }
    const status = statusForTerminalOutput(output);
    if (!status) {
      return;
    }
    dispatch(showTerminalMessage(status, true));
  };

export const hideTerminalMessageIfActive =
  (sessionId: number): AppThunk =>
  (dispatch, getState) => {
    const state = getState();
    if (sessionId !== state.terminal.sessionId) {
      return;
    }
    if (state.terminalStatus.terminalMessage && !state.terminalStatus.terminalCopyOutput) {
      dispatch(hideTerminalMessage());
    }
  };

// handleDoctorCompleted relies on the `doctor-completed` event, since doctor runs
// in the shared Local shell and never produces a PTY exit.
export const handleDoctorCompleted =
  (payload: DoctorCompletedPayload): AppThunk =>
  (dispatch) => {
    dispatch(
      recordDoctorOutcome({
        key: selectionKey({ tenant: payload.tenant, environment: payload.environment }),
        outcome: {
          ranAt: Date.now(),
          success: payload.success,
          message: (payload.message ?? '').trim(),
        },
      }),
    );
  };

// handleSSHDInitCompleted relies on `sshd-init-completed` for the same reason.
export const handleSSHDInitCompleted =
  (payload: SSHDInitCompletedPayload): AppThunk =>
  (dispatch) => {
    dispatch(
      recordSSHDInitOutcome({
        key: selectionKey({ tenant: payload.tenant, environment: payload.environment }),
        outcome: {
          ranAt: Date.now(),
          success: payload.success,
          message: (payload.message ?? '').trim(),
        },
      }),
    );
  };

const dropExitedSessionFromTabs =
  (sessionId: number, openExitSelection: UISelection | undefined): AppThunk =>
  (dispatch, getState) => {
    if (!openExitSelection) {
      return;
    }
    const key = selectionKey(openExitSelection);
    const remaining = dispatch(removeTab(key, sessionId));
    if (getState().terminal.sessionId !== sessionId) {
      return;
    }
    if (envClosing(getState().sessions, key)) {
      // The env is closing; auto-selecting a sibling would respawn a closed tab.
      return;
    }
    const next = remaining[remaining.length - 1];
    if (next) {
      dispatch(selectTerminalTab(next.sessionId));
    }
  };

const computeTerminalExitReason = (
  payload: TerminalExitPayload,
  selections: TerminalExitSelections,
): string => {
  if (payload.reason) {
    return failedTerminalExitReason(payload.reason, selections);
  }
  return successfulTerminalExitReason(selections);
};

export const handleTerminalExit =
  (payload: TerminalExitPayload): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    // takeExitSelections reads and clears the metadata in one dispatch.
    const selections = controller.sessions.takeExitSelections(payload.sessionId);
    const reason = computeTerminalExitReason(payload, selections);
    const failedOutput = recordTerminalExit(dispatch, controller, payload, selections, reason);

    dispatch(dropExitedSessionFromTabs(payload.sessionId, selections.openSelection));
    dispatch(releaseExitedSession(payload.sessionId));

    // Not this pane's session, or a deliberate teardown the operator asked for.
    if (payload.deliberate === true || payload.sessionId !== getState().terminal.sessionId) {
      return;
    }
    dispatchTerminalExitFeedback(dispatch, payload, selections, reason, failedOutput);
  };

function recordTerminalExit(
  dispatch: AppDispatch,
  controller: NonNullable<ReturnType<typeof requireController>>,
  payload: TerminalExitPayload,
  selections: TerminalExitSelections,
  reason: string,
): string {
  dispatch(recordExitReason({ sessionId: payload.sessionId, reason }));
  if (!payload.reason || !terminalExitHasTrackedSelection(selections)) {
    return '';
  }
  const failedOutput = failedTerminalOutput(controller.sessions, payload.sessionId, reason);
  if (failedOutput) {
    dispatch(recordExitOutput({ sessionId: payload.sessionId, output: failedOutput }));
  }
  return failedOutput;
}

function dispatchTerminalExitFeedback(
  dispatch: AppDispatch,
  payload: TerminalExitPayload,
  selections: TerminalExitSelections,
  reason: string,
  failedOutput: string,
): void {
  if (payload.reason && terminalExitHasTrackedSelection(selections)) {
    const failure = classifiedTerminalFailure(
      payload.reason,
      reason,
      failedOutput,
      selections.openSelection,
    );
    dispatch(
      showTerminalFailure(
        failure.message,
        failure.detail,
        failedOutput,
        failure.action,
        failure.retrySelection,
      ),
    );
    return;
  }
  dispatch(showTerminalMessage(reason));
}

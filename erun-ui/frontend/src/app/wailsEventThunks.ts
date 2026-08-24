import type { TerminalExitPayload, UISelection } from '@/types';

import { reloadStateAfterEnvironmentChange } from './bootThunks';
import { environmentTypeIsRemoteWorktree } from './environmentType';
import { readError } from './errors';
import type {
  AIActivityPayload,
  AppNotificationPayload,
  AppStatusPayload,
  DoctorCompletedPayload,
  EnvActivityPayload,
  EnvironmentInitializedPayload,
  EnvStatusPayload,
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
import { setEnvActivityForEnv, setEnvStatusForEnv } from './slices/envStatusSlice';
import { setShellActivityForSession } from './slices/orchestratorShellActivitySlice';
import { appendReconnectLine } from './slices/reviewSlice';
import {
  clearPendingOpenAfterDeploy,
  setPendingOpenAfterDeploy,
  setSelected,
} from './slices/selectionSlice';
import { recordExitOutput, recordExitReason } from './slices/sessionsSlice';
import { recordSSHDInitOutcome } from './slices/sshdInitSlice';
import type { AppDispatch, AppThunk } from './store';
import { removeTab } from './tabsThunks';
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

// Every Wails event the controller subscribes to is handled here as a
// state-side thunk — except terminal-output, which stays imperative on the
// controller because both the registry buffers and the live xterm instance
// live there.

// handleAIActivity surfaces that an AI tab is working in one env while the
// user is looking at another (Nielsen #1, visibility of system status).
export const handleAIActivity =
  (payload: AIActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      // An orchestrator session carries no env to key by. Dropping the event
      // here is why the orchestrator row never spun while it was working.
      if (payload.sessionId > 0) {
        dispatch(setAIBusyForSession({ sessionId: payload.sessionId, busy: payload.busy }));
      }
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setAIBusyForEnv({ key, busy: payload.busy }));
  };

// handleOrchestratorShellActivity surfaces that an orchestrator has a
// background shell running even after its own turn has gone idle — the case a
// plain busy spinner cannot show, since backgrounding a shell is what lets the
// turn end without waiting for it.
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

// handleEnvStatus keeps the sidebar's open dot reflecting the env's real
// condition, not mere tab presence (Nielsen #2, match between system and
// real state).
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

// handleEnvActivity records what the environment itself reports — its edge
// answering, and whether work is in flight — so a row driven from the CLI or by
// an in-pod agent shows its condition instead of rendering blank (Nielsen #1,
// visibility of system status).
export const handleEnvActivity =
  (payload: EnvActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(
      setEnvActivityForEnv({
        key,
        activity: {
          reachable: payload.reachable,
          observed: payload.observed,
          outage: payload.outage === true,
          busy: payload.busy,
          detail: (payload.detail ?? '').trim(),
        },
      }),
    );
  };

// handleAppStatus surfaces a backend status line to the user.
export const handleAppStatus =
  (payload: AppStatusPayload): AppThunk =>
  (dispatch) => {
    const message = (payload.message ?? '').trim();
    if (!message) {
      return;
    }
    dispatch(showTerminalMessage(message, payload.busy === true));
  };

// handleAppNotification shows one-shot info/success events as a transient
// toast; they would go stale if left on the persistent titlebar pill.
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
      }),
    );
  };

// Bounded retry budget for surfacing a just-initialized env. A single reload
// can miss it — a best-effort getInitialState failure, or a reload that
// coalesced with the fsnotify watcher's refresh — leaving the sidebar stale
// with no other recovery. Retrying self-heals the missed refresh instead of
// silently dropping the new environment (erun-ui/AGENTS.md § "Command
// Completion And State-Refresh Wiring").
const ENVIRONMENT_INIT_RELOAD_ATTEMPTS = 3;
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

// Fires when the backend observes `==> Initialized <tenant>/<env>` (or the
// config watcher detects a new env). What happens next depends on whether
// `erun init` already deployed the runtime:
//   - remote-worktree envs (remote-agent / runtime): init deploys the runtime
//     itself — carrying MCP-auth + the resolved registry — and waits for it to
//     become Available before emitting this, so we OPEN directly. Composing a
//     second deploy here re-rendered the chart (MCP-auth + cluster registry) and
//     rolled the pod init had just created.
//   - local-agent (builds-here) envs: init does NOT deploy (no in-pod build), so
//     the desktop composes the single build→push→deploy and opens the env's tabs
//     on the matching `environment-deployed` signal (handleEnvironmentDeployed).
// See erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring".
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
    if (environmentTypeIsRemoteWorktree(selectEnvironmentType(getState(), tenant, environment))) {
      // init already deployed + waited; open directly.
      try {
        await dispatch(openSelection({ tenant, environment }));
      } catch (error) {
        dispatch(showTerminalError(readError(error)));
      }
      return;
    }
    // local-agent: init did not deploy, so compose the single deploy and gate the
    // open on environment-deployed.
    dispatch(setPendingOpenAfterDeploy({ tenant, environment }));
    try {
      await dispatch(startInitialDeploySelection({ tenant, environment }));
    } catch (error) {
      dispatch(clearPendingOpenAfterDeploy());
      dispatch(showTerminalError(readError(error)));
    }
  };

// Fires on a successful or skipped deploy. Gates the create→deploy→open flow:
// opens the env's tabs only if it was queued pending-open at create time; any
// other deploy (the Deploy button, a manual redeploy) has no pending entry and
// is a no-op, so an env auto-opens only when the user created it.
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

// Fires on an init failure. Surfaces an error toast and reverts the optimistic
// state.selected so the sidebar's "creating ..." placeholder row disappears.
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

// handleReconnectLine buffers a reconnect PTY status line while the reconnect
// is running.
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

// updateOpenStatusFromOutput promotes a recognised status fragment from an
// opening env's terminal output into the busy-state message.
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

// hideTerminalMessageIfActive clears the busy message once real output
// resumes on the active session.
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

// handleDoctorCompleted records `erun doctor`'s last-run outcome for the
// Manage dialog's SSH tab. This is doctor's only completion signal — it runs
// piped into the shared Local shell, which never produces a PTY exit (see
// erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring") — so a
// handler keyed on terminal exit can never fire; the `doctor-completed` Wails
// event fires from the CLI's `==> Doctor done` / `==> Doctor failed` trace
// lines instead (see handleDoctorTraceLine in erun-ui/activity_queue_app.go).
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

// handleSSHDInitCompleted records `erun sshd init`'s last-run outcome for the
// Manage dialog's SSH access section. Like doctor, sshd init runs piped into
// the shared Local shell and never produces a PTY exit, so the
// `sshd-init-completed` Wails event — fired from the CLI's `==> SSHD init
// done` / `==> SSHD init failed` trace lines (see handleSSHDInitTraceLine in
// erun-ui/activity_queue_app.go) — is its only completion signal.
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
    // takeExitSelections both reads the per-session metadata AND clears it
    // in one atomic dispatch — pull the values out first.
    const selections = controller.sessions.takeExitSelections(payload.sessionId);
    const reason = computeTerminalExitReason(payload, selections);
    const failedOutput = recordTerminalExit(dispatch, controller, payload, selections, reason);

    dispatch(dropExitedSessionFromTabs(payload.sessionId, selections.openSelection));

    if (payload.sessionId !== getState().terminal.sessionId) {
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

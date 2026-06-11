import type { TerminalExitPayload, UISelection } from '@/types';

import { reloadStateAfterEnvironmentChange } from './bootThunks';
import { appendDebugOutput } from './debugThunks';
import { readError } from './errors';
import type {
  AIActivityPayload,
  AppNotificationPayload,
  AppStatusPayload,
  EnvironmentInitializedPayload,
  EnvStatusPayload,
  TerminalExitSelections,
} from './model';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { selectEnvironmentExists, selectSelectedIsPendingFor } from './selectors';
import { openSelection, selectTerminalTab } from './sessionThunks';
import { setAIBusyForEnv } from './slices/aiActivitySlice';
import { setDoctorAll } from './slices/doctorSlice';
import { setEnvStatusForEnv } from './slices/envStatusSlice';
import { appendReconnectLine } from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import { recordExitOutput, recordExitReason } from './slices/sessionsSlice';
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

// wailsEventThunks own the state-side handling of every Wails event the
// controller subscribes to. The controller's mount() arms the EventsOn
// callbacks; each callback dispatches one of these thunks. The lone
// exception is terminal-output, which still does its imperative xterm
// write on the controller because the registry buffers + the live xterm
// instance both live there.

// handleAIActivity flips the per-env "AI tab is working" latch driven by
// the Go-side debounced ai-activity event. The sidebar's env row picks
// it up via deriveEnvironmentRow so a Claude/Codex generation in env A
// is visible while the user is looking at env B. Nielsen #1 (visibility
// of system status). See erun-ui/terminal_sessions.go: recordAIActivity
// for the debounce policy.
export const handleAIActivity =
  (payload: AIActivityPayload): AppThunk =>
  (dispatch) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    const key = selectionKey({ tenant, environment });
    dispatch(setAIBusyForEnv({ key, busy: payload.busy }));
  };

// handleEnvStatus records the env's real condition behind the sidebar's open
// dot (issue #470): the Go side flags a row 'stopped' when reconnect is
// refused because the linked cloud context is not running, 'failed' when a
// deploy failure / reconnect loop ends retries, and clears the flag ('') on
// every fresh open attempt and successful respawn. Match between system and
// real state (Nielsen #2): the dot must not claim "running" on tab presence
// alone.
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

// handleAppStatus surfaces backend status lines as a busy-state terminal
// message and mirrors them into the debug pane for forensics.
export const handleAppStatus =
  (payload: AppStatusPayload): AppThunk =>
  (dispatch) => {
    const message = (payload.message ?? '').trim();
    if (!message) {
      return;
    }
    dispatch(appendDebugOutput(`[status] ${message}\n`));
    dispatch(showTerminalMessage(message, payload.busy === true));
  };

// handleAppNotification routes a transient toast emitted from the Go
// side through the auto-dismissing notification slot. Used for one-shot
// info/success events (e.g. "Stopped idle cloud context X.") that
// would go stale if left on the persistent titlebar pill — see
// erun-ui/AGENTS.md § "UX Impact Review Checklist" item 3 and issue
// #361.
export const handleAppNotification =
  (payload: AppNotificationPayload): AppThunk =>
  (dispatch) => {
    const message = (payload.message ?? '').trim();
    if (!message) {
      return;
    }
    const kind = payload.kind ?? 'info';
    dispatch(appendDebugOutput(`[notification:${kind}] ${message}\n`));
    dispatch(showNotification(kind, message));
  };

// Fires when the backend's PTY trace handler observes
// `==> Initialized <tenant>/<env>` from a piped `erun init` command, or
// when the config-file watcher detects a new env. Reload state so the new
// env appears in the sidebar, surface a success toast (Nielsen #1 system
// status visibility), then open the selection so the ERun and AI tabs
// spawn against the now-existing config. See erun-ui/AGENTS.md §
// "Command Completion And State-Refresh Wiring".
export const handleEnvironmentInitialized =
  (payload: EnvironmentInitializedPayload): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const tenant = payload.tenant.trim();
    const environment = payload.environment.trim();
    if (!tenant || !environment) {
      return;
    }
    await dispatch(reloadStateAfterEnvironmentChange());
    if (!selectEnvironmentExists(getState(), tenant, environment)) {
      return;
    }
    dispatch(showNotification('success', `Created ${tenant} / ${environment}.`));
    try {
      await dispatch(openSelection({ tenant, environment }));
    } catch (error) {
      dispatch(showTerminalMessage(readError(error)));
    }
  };

// Fires when the backend's PTY trace handler observes `==> Initialization
// failed <tenant>/<env>`. Surfaces an error toast (Nielsen #1 + #9) and
// reverts the optimistic state.selected so the sidebar's "creating ..."
// placeholder row disappears.
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

// handleReconnectLine appends a status line from the reconnect PTY into
// the reconnect line buffer while it is running. The subscription wiring
// stays on the controller; this thunk owns the state write.
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

// updateOpenStatusFromOutput inspects a freshly-decoded chunk of terminal
// output and, when the session is an open-env session that has not yet
// landed on its prompt, promotes a recognised status fragment into the
// busy-state terminal message. The output buffer is the source of truth;
// this is purely state.
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

// hideTerminalMessageIfActive hides the busy message when fresh output
// arrives for the active session and no copy-output is set. Called from
// the imperative terminal-output handler on the controller.
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

const recordDoctorOutcome =
  (payload: TerminalExitPayload, selections: TerminalExitSelections): AppThunk =>
  (dispatch, getState) => {
    const selection = selections.doctorSelection;
    if (!selection) {
      return;
    }
    const key = selectionKey(selection);
    const reason = (payload.reason ?? '').trim();
    const lastDoctorBySelection = getState().doctor.lastDoctorBySelection;
    dispatch(
      setDoctorAll({
        lastDoctorBySelection: {
          ...lastDoctorBySelection,
          [key]: {
            ranAt: Date.now(),
            success: !reason,
            message: reason,
          },
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
  (payload: TerminalExitPayload): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    // takeExitSelections both reads the per-session metadata AND clears it
    // in one atomic dispatch — pull the values out first.
    const selections = controller.sessions.takeExitSelections(payload.sessionId);
    const reason = computeTerminalExitReason(payload, selections);
    const failedOutput = recordTerminalExit(dispatch, controller, payload, selections, reason);

    dispatch(dropExitedSessionFromTabs(payload.sessionId, selections.openSelection));
    dispatch(recordDoctorOutcome(payload, selections));

    if (selections.sshdInitSelection) {
      await dispatch(reloadStateAfterEnvironmentChange());
    }
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
  if (!payload.reason && selections.sshdInitSelection) {
    dispatch(showTerminalMessage(reason));
    return;
  }
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

import type { UISelection } from '@/types';

import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { readError } from './errors';
import { openSelection } from './sessionThunks';
import {
  dismissNotification as dismissNotificationAction,
  showNotification as showNotificationAction,
} from './slices/notificationSlice';
import {
  clearTerminalStatus,
  setRetrySelection,
  setTerminalCopyOutput,
  setTerminalCopyStatus,
  setTerminalMessage,
} from './slices/terminalStatusSlice';
import type { AppNotification, TerminalStatusAction } from './state';
import type { AppThunk } from './store';
import { TRANSIENT_DISMISS_MS } from './transientDismissDuration';
import { scheduleTransientDismiss } from './transientDismissTimer';

// notificationIdCounter mints a unique id per queued notification within this
// session so a specific entry's auto-dismiss timer or explicit dismiss click
// can target it without disturbing sibling entries queued before or after it.
let notificationIdCounter = 0;
function nextNotificationId(): string {
  notificationIdCounter += 1;
  return `notification-${notificationIdCounter.toString()}`;
}
let terminalCopyStatusTimer = 0;

export const showTerminalMessage =
  (message: string, busy = false): AppThunk =>
  (dispatch) => {
    dispatch(setTerminalMessage({ message, busy, kind: 'info', detail: '', actionKind: '' }));
    if (busy) {
      dispatch(setTerminalCopyOutput(''));
      dispatch(setTerminalCopyStatus(''));
    }
    dispatch(setRetrySelection(null));
  };

// showTerminalFailure's copyOutput is taken exactly as given: empty means the
// caller has no captured command output to offer, and the titlebar renders
// no Copy action for it. A bare validation/precondition message — "tenant is
// required" and its kin — already says everything there is to say once it
// names the operation and the recovery, so re-copying that same sentence as
// fake "output" earns the operator nothing (root AGENTS.md "Smooth,
// Seamless, No Dead Ends" — an affordance that does nothing is worse than no
// affordance). A caller that genuinely captured command output (a failed IDE
// launch, a delete's namespace-cleanup warning) passes it explicitly.
export const showTerminalFailure =
  (
    message: string,
    detail: string,
    copyOutput: string,
    action: TerminalStatusAction,
    retrySelection: UISelection | null,
  ): AppThunk =>
  (dispatch) => {
    dispatch(
      setTerminalMessage({
        message,
        busy: false,
        kind: action === 'wait-longer' ? 'warning' : 'error',
        detail,
        actionKind: action,
      }),
    );
    dispatch(setTerminalCopyOutput(copyOutput));
    dispatch(setTerminalCopyStatus(''));
    dispatch(setRetrySelection(action === 'wait-longer' ? retrySelection : null));
  };

// showTerminalError is the one-argument shorthand for the common
// `catch (error) { dispatch(showTerminalError(readError(error))) }` shape.
// Dozens of catch blocks used to call showTerminalMessage (kind: 'info',
// unconditional) with the caught error's text, so failures rendered as a
// neutral grey pill — polite aria-live, no role="alert", and no Copy action
// (the terminal copy buffer only gets set by showTerminalFailure). This
// wraps showTerminalFailure with the defaults that shape needs: no detail
// beyond the message, no captured output to copy (the message is already
// fully visible in the pill/popover), and no retry action.
export const showTerminalError =
  (message: string): AppThunk =>
  (dispatch) => {
    dispatch(showTerminalFailure(message, '', '', '', null));
  };

export const hideTerminalMessage = (): AppThunk => (dispatch) => {
  dispatch(clearTerminalStatus());
  dispatch(setTerminalCopyOutput(''));
  dispatch(setTerminalCopyStatus(''));
  dispatch(setRetrySelection(null));
};

export const dismissTerminalStatus = (): AppThunk => (dispatch, getState) => {
  const status = getState().terminalStatus;
  if (
    !status.terminalMessage &&
    !status.terminalStatusDetail &&
    !status.terminalCopyOutput &&
    !status.terminalCopyStatus
  ) {
    return;
  }
  dispatch(clearTerminalStatus());
  dispatch(setTerminalCopyOutput(''));
  dispatch(setTerminalCopyStatus(''));
  dispatch(setRetrySelection(null));
};

export const showNotification =
  (
    kind: AppNotification['kind'],
    message: string,
    meta?: {
      tenant?: string;
      environment?: string;
      source?: string;
      orchestratorId?: string;
      action?: AppNotification['action'];
    },
  ): AppThunk =>
  (dispatch) => {
    const trimmed = message.trim();
    if (!trimmed) {
      return;
    }
    const id = nextNotificationId();
    dispatch(
      showNotificationAction({
        id,
        kind,
        message: trimmed,
        timestamp: Date.now(),
        dismissed: false,
        tenant: meta?.tenant,
        environment: meta?.environment,
        source: meta?.source,
        orchestratorId: meta?.orchestratorId,
        action: meta?.action,
      }),
    );
    if (kind === 'success' || kind === 'info') {
      // Bound to this entry's own id, not a shared timer: a second toast
      // queued before this one auto-dismisses must not have its timer
      // clobbered (and must not, in turn, dismiss this one early).
      scheduleTransientDismiss(TRANSIENT_DISMISS_MS, () => {
        dispatch(dismissNotificationAction(id));
      });
    }
  };

// dismissNotification marks one history entry read by id. It never removes
// the entry -- see AppNotification's own doc comment -- so the message
// centre dialog keeps showing it for the rest of the session.
export const dismissNotification =
  (id: string): AppThunk =>
  (dispatch) => {
    dispatch(dismissNotificationAction(id));
  };

export const waitLongerForTerminalStatus =
  (): AppThunk<Promise<void>> => async (dispatch, getState) => {
    const selection = getState().terminalStatus.retrySelection;
    if (!selection) {
      return;
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(
      showTerminalMessage(
        `Waiting longer for ${selection.tenant} / ${selection.environment}...`,
        true,
      ),
    );
    await dispatch(openSelection(selection));
  };

export const copyTerminalOutput = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const copyOutput = getState().terminalStatus.terminalCopyOutput;
  if (!copyOutput) {
    return;
  }
  try {
    await ClipboardSetText(copyOutput);
    dispatch(setTerminalCopyStatus('Copied'));
  } catch (error) {
    dispatch(setTerminalCopyStatus(readError(error)));
  }
  window.clearTimeout(terminalCopyStatusTimer);
  terminalCopyStatusTimer = window.setTimeout(() => {
    dispatch(setTerminalCopyStatus(''));
  }, 1400);
};

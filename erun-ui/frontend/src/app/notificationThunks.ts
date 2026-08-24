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
    // Some errors (e.g. AWS API strings) arrive with no terminal output; copy
    // the message itself so the operator can paste the full error — which the
    // titlebar pill truncates — into a bug report. Nielsen #9 (recovery from errors).
    const effectiveCopy = copyOutput || joinMessageForCopy(message, detail);
    dispatch(setTerminalCopyOutput(effectiveCopy));
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
// beyond the message, the message itself as the copyable text, and no retry
// action.
export const showTerminalError =
  (message: string): AppThunk =>
  (dispatch) => {
    dispatch(showTerminalFailure(message, '', message, '', null));
  };

function joinMessageForCopy(message: string, detail: string): string {
  const trimmedMessage = message.trim();
  const trimmedDetail = detail.trim();
  if (!trimmedMessage && !trimmedDetail) {
    return '';
  }
  if (!trimmedDetail) {
    return trimmedMessage;
  }
  if (!trimmedMessage) {
    return trimmedDetail;
  }
  return `${trimmedMessage}. ${trimmedDetail}`;
}

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
    meta?: { tenant?: string; environment?: string; source?: string },
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
        tenant: meta?.tenant,
        environment: meta?.environment,
        source: meta?.source,
      }),
    );
    if (kind === 'success' || kind === 'info') {
      // Bound to this entry's own id, not a shared timer: a second toast
      // queued before this one auto-dismisses must not have its timer
      // clobbered (and must not, in turn, dismiss this one early).
      window.setTimeout(() => {
        dispatch(dismissNotificationAction(id));
      }, 3200);
    }
  };

// dismissNotification dismisses a specific queued entry by id, or — from the
// titlebar's dismiss button, which only ever shows the front of the queue —
// the oldest entry when no id is given.
export const dismissNotification =
  (id?: string): AppThunk =>
  (dispatch, getState) => {
    const notifications = getState().notification.notifications;
    const target = id ?? notifications[0]?.id;
    if (!target) {
      return;
    }
    dispatch(dismissNotificationAction(target));
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

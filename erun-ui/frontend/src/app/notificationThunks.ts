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

let notificationTimer = 0;
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
    window.clearTimeout(notificationTimer);
    dispatch(
      showNotificationAction({
        kind,
        message: trimmed,
        tenant: meta?.tenant,
        environment: meta?.environment,
        source: meta?.source,
      }),
    );
    if (kind === 'success' || kind === 'info') {
      notificationTimer = window.setTimeout(() => {
        dispatch(dismissNotification());
      }, 3200);
    }
  };

export const dismissNotification = (): AppThunk => (dispatch, getState) => {
  window.clearTimeout(notificationTimer);
  if (!getState().notification.notification) {
    return;
  }
  dispatch(dismissNotificationAction());
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

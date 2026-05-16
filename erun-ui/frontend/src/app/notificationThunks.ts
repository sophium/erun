import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { readError } from './errors';
import {
  dismissNotification as dismissNotificationAction,
  showNotification as showNotificationAction,
} from './slices/notificationSlice';
import {
  clearTerminalStatus,
  setTerminalCopyOutput,
  setTerminalCopyStatus,
  setTerminalMessage,
} from './slices/terminalStatusSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import type { AppNotification, TerminalStatusAction } from './state';
import type { UISelection } from '@/types';

// notificationThunks own the titlebar terminal-status message lifecycle and
// the toast-style notification slot. The timers and the wait-longer retry
// target previously lived on the controller; module-level state is sufficient
// for the singleton-controller case.

let notificationTimer = 0;
let terminalCopyStatusTimer = 0;
let terminalStatusRetrySelection: UISelection | null = null;

export function getTerminalStatusRetrySelection(): UISelection | null {
  return terminalStatusRetrySelection;
}

export const showTerminalMessage = (
  message: string,
  busy = false,
): AppThunk => (dispatch) => {
  dispatch(setTerminalMessage({ message, busy, kind: 'info', detail: '', actionKind: '' }));
  if (busy) {
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
  }
  terminalStatusRetrySelection = null;
};

export const showTerminalFailure = (
  message: string,
  detail: string,
  copyOutput: string,
  action: TerminalStatusAction,
  retrySelection: UISelection | null,
): AppThunk => (dispatch) => {
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
  terminalStatusRetrySelection = action === 'wait-longer' ? retrySelection : null;
};

export const hideTerminalMessage = (): AppThunk => (dispatch) => {
  dispatch(clearTerminalStatus());
  dispatch(setTerminalCopyOutput(''));
  dispatch(setTerminalCopyStatus(''));
  terminalStatusRetrySelection = null;
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
  terminalStatusRetrySelection = null;
};

export const showNotification = (
  kind: AppNotification['kind'],
  message: string,
): AppThunk => (dispatch) => {
  const trimmed = message.trim();
  if (!trimmed) {
    return;
  }
  window.clearTimeout(notificationTimer);
  dispatch(showNotificationAction({ kind, message: trimmed }));
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

export const waitLongerForTerminalStatus = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = terminalStatusRetrySelection;
    if (!selection) {
      return;
    }
    dispatch(setTerminalCopyOutput(''));
    dispatch(setTerminalCopyStatus(''));
    dispatch(showTerminalMessage(`Waiting longer for ${selection.tenant} / ${selection.environment}...`, true));
    await controller.openSelection(selection);
  };

export const copyTerminalOutput = (): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
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

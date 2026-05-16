import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { readError } from './errors';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import type { AppState, TerminalStatusAction } from './state';
import type { UISelection } from '@/types';

// notificationThunks own the titlebar terminal-status message lifecycle and
// the toast-style notification slot. The timers and the wait-longer retry
// target previously lived on ERunUIController fields; module-level state
// is sufficient for the singleton-controller case.

let notificationTimer = 0;
let terminalCopyStatusTimer = 0;
let terminalStatusRetrySelection: UISelection | null = null;

export function getTerminalStatusRetrySelection(): UISelection | null {
  return terminalStatusRetrySelection;
}

export const showTerminalMessage = (
  message: string,
  busy = false,
): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.terminalMessage = message;
  controller.state.terminalStatusKind = 'info';
  controller.state.terminalStatusDetail = '';
  controller.state.terminalStatusAction = '';
  controller.state.terminalBusy = busy;
  if (busy) {
    controller.state.terminalCopyOutput = '';
    controller.state.terminalCopyStatus = '';
  }
  terminalStatusRetrySelection = null;
};

export const showTerminalFailure = (
  message: string,
  detail: string,
  copyOutput: string,
  action: TerminalStatusAction,
  retrySelection: UISelection | null,
): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.terminalMessage = message;
  controller.state.terminalStatusKind = action === 'wait-longer' ? 'warning' : 'error';
  controller.state.terminalStatusDetail = detail;
  controller.state.terminalStatusAction = action;
  controller.state.terminalBusy = false;
  controller.state.terminalCopyOutput = copyOutput;
  controller.state.terminalCopyStatus = '';
  terminalStatusRetrySelection = action === 'wait-longer' ? retrySelection : null;
};

export const hideTerminalMessage = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  controller.state.terminalMessage = '';
  controller.state.terminalStatusKind = 'info';
  controller.state.terminalStatusDetail = '';
  controller.state.terminalStatusAction = '';
  controller.state.terminalBusy = false;
  controller.state.terminalCopyOutput = '';
  controller.state.terminalCopyStatus = '';
  terminalStatusRetrySelection = null;
};

export const dismissTerminalStatus = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  if (
    !controller.state.terminalMessage &&
    !controller.state.terminalStatusDetail &&
    !controller.state.terminalCopyOutput &&
    !controller.state.terminalCopyStatus
  ) {
    return;
  }
  controller.state.terminalMessage = '';
  controller.state.terminalStatusKind = 'info';
  controller.state.terminalStatusDetail = '';
  controller.state.terminalStatusAction = '';
  controller.state.terminalBusy = false;
  controller.state.terminalCopyOutput = '';
  controller.state.terminalCopyStatus = '';
  terminalStatusRetrySelection = null;
};

export const showNotification = (
  kind: NonNullable<AppState['notification']>['kind'],
  message: string,
): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  const trimmed = message.trim();
  if (!trimmed) {
    return;
  }
  window.clearTimeout(notificationTimer);
  controller.state.notification = {
    kind,
    message: trimmed,
  };
  if (kind === 'success' || kind === 'info') {
    notificationTimer = window.setTimeout(() => {
      dispatch(dismissNotification());
    }, 3200);
  }
};

export const dismissNotification = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  window.clearTimeout(notificationTimer);
  if (!controller.state.notification) {
    return;
  }
  controller.state.notification = null;
};

export const waitLongerForTerminalStatus = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = terminalStatusRetrySelection;
    if (!selection) {
      return;
    }
    controller.state.terminalStatusAction = '';
    controller.state.terminalCopyOutput = '';
    controller.state.terminalCopyStatus = '';
    dispatch(showTerminalMessage(`Waiting longer for ${selection.tenant} / ${selection.environment}...`, true));
    await controller.openSelection(selection);
  };

export const copyTerminalOutput = (): AppThunk<Promise<void>> =>
  async (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (!controller.state.terminalCopyOutput) {
      return;
    }
    try {
      await ClipboardSetText(controller.state.terminalCopyOutput);
      controller.state.terminalCopyStatus = 'Copied';
    } catch (error) {
      controller.state.terminalCopyStatus = readError(error);
    }
    window.clearTimeout(terminalCopyStatusTimer);
    terminalCopyStatusTimer = window.setTimeout(() => {
      controller.state.terminalCopyStatus = '';
    }, 1400);
  };

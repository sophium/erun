import type { UISelection } from '@/types';

import { ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { readError } from './errors';
import type { NotificationFilter } from './notificationCenter';
import { openSelection } from './sessionThunks';
import {
  dismissNotification as dismissNotificationAction,
  dismissNotifications as dismissNotificationsAction,
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

// showTerminalFailure: empty copyOutput means no Copy action; never copy the
// message itself as fake output.
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

// showTerminalError renders a caught error as an error, with nothing to copy.
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
      // Per-entry timer so queued toasts do not dismiss each other.
      scheduleTransientDismiss(TRANSIENT_DISMISS_MS, () => {
        dispatch(dismissNotificationAction(id));
      });
    }
  };

// dismissNotification marks read; history keeps the entry.
export const dismissNotification =
  (id: string): AppThunk =>
  (dispatch) => {
    dispatch(dismissNotificationAction(id));
  };

export const markAllNotificationsRead =
  (filter: NotificationFilter): AppThunk =>
  (dispatch) => {
    dispatch(dismissNotificationsAction(filter));
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

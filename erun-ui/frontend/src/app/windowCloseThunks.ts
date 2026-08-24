import { ConfirmWindowClose, ConsumeInterruptedActivityNotice } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import {
  dismissCloseGate,
  setCloseGateConfirming,
  setCloseGateError,
} from './slices/closeGateSlice';
import type { AppThunk } from './store';

// cancelCloseGate is the operator choosing not to close: the window stays
// open and every running job keeps running untouched.
export const cancelCloseGate = (): AppThunk => (dispatch) => {
  dispatch(dismissCloseGate());
};

// confirmCloseGate is the operator's explicit "close anyway": it records
// what is being interrupted, then quits. The desktop process exits shortly
// after a successful call, so there is normally nothing left to dispatch —
// the busy/error state below exists for the write-failure case, where the
// app still quits but the dialog briefly shows why the record could not be
// saved before the window actually closes.
export const confirmCloseGate = (): AppThunk<Promise<void>> => async (dispatch) => {
  dispatch(setCloseGateConfirming(true));
  try {
    await ConfirmWindowClose();
  } catch (error: unknown) {
    dispatch(setCloseGateError(readError(error)));
  }
};

// loadInterruptedActivityNotice runs once at boot, right after LoadState, and
// surfaces work a previous launch's confirmed close interrupted — so a
// relaunch after that can say what was lost instead of starting blank.
interface InterruptedActivityEntry {
  command: string;
  tenant: string;
  environment: string;
}

export const loadInterruptedActivityNotice = (): AppThunk<Promise<void>> => async (dispatch) => {
  // The Wails binding's generated type claims a non-null array, but the Go
  // side returns nil (JSON null) when there is nothing to report.
  const entries = (await ConsumeInterruptedActivityNotice().catch((error: unknown) => {
    console.error('loadInterruptedActivityNotice failed:', readError(error));
    return null;
  })) as InterruptedActivityEntry[] | null;
  if (!entries || entries.length === 0) {
    return;
  }
  const names = entries.map((entry) => `${entry.command} (${entry.tenant}/${entry.environment})`);
  dispatch(
    showNotification(
      'warning',
      `Closing the app earlier interrupted: ${names.join(', ')}. Check their state before assuming they finished.`,
    ),
  );
};

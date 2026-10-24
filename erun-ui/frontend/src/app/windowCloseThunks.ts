import { ConfirmWindowClose, ConsumeInterruptedActivityNotice } from '../../wailsjs/go/main/App';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import {
  dismissCloseGate,
  setCloseGateConfirming,
  setCloseGateError,
} from './slices/closeGateSlice';
import type { AppThunk } from './store';

export const cancelCloseGate = (): AppThunk => (dispatch) => {
  dispatch(dismissCloseGate());
};

// confirmCloseGate's error state only shows if recording the interruption fails.
export const confirmCloseGate = (): AppThunk<Promise<void>> => async (dispatch) => {
  dispatch(setCloseGateConfirming(true));
  try {
    await ConfirmWindowClose();
  } catch (error: unknown) {
    dispatch(setCloseGateError(readError(error)));
  }
};

interface InterruptedActivityEntry {
  command: string;
  tenant: string;
  environment: string;
}

export const loadInterruptedActivityNotice = (): AppThunk<Promise<void>> => async (dispatch) => {
  // Go returns null when empty, despite the generated non-null type.
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

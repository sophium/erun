import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UISelection } from '@/types';

import type { TerminalStatusAction, TerminalStatusKind } from '../state';

export interface TerminalStatusState {
  terminalMessage: string;
  terminalStatusKind: TerminalStatusKind;
  terminalStatusDetail: string;
  terminalStatusAction: TerminalStatusAction;
  terminalBusy: boolean;
  terminalCopyOutput: string;
  terminalCopyStatus: string;
  retrySelection: UISelection | null;
  // Per-session dismissals of the activity-lock overlay. Stored as a
  // Record<number, true> so the state stays serializable; the original
  // useState held a Set<number>.
  hiddenLockSessions: Record<number, true>;
  // Debug-panel "Copied"/error transient. The terminal copy status above
  // is for the main terminal output copy button; this is the debug panel.
  debugCopyStatus: string;
}

const initialState: TerminalStatusState = {
  terminalMessage: '',
  terminalStatusKind: 'info',
  terminalStatusDetail: '',
  terminalStatusAction: '',
  terminalBusy: false,
  terminalCopyOutput: '',
  terminalCopyStatus: '',
  retrySelection: null,
  hiddenLockSessions: {},
  debugCopyStatus: '',
};

export const terminalStatusSlice = createSlice({
  name: 'terminalStatus',
  initialState,
  reducers: {
    setTerminalMessage(
      state,
      action: PayloadAction<{
        message: string;
        busy?: boolean;
        kind?: TerminalStatusKind;
        detail?: string;
        actionKind?: TerminalStatusAction;
      }>,
    ) {
      state.terminalMessage = action.payload.message;
      state.terminalBusy = action.payload.busy ?? false;
      state.terminalStatusKind = action.payload.kind ?? 'info';
      state.terminalStatusDetail = action.payload.detail ?? '';
      state.terminalStatusAction = action.payload.actionKind ?? '';
    },
    clearTerminalStatus(state) {
      state.terminalMessage = '';
      state.terminalBusy = false;
      state.terminalStatusKind = 'info';
      state.terminalStatusDetail = '';
      state.terminalStatusAction = '';
    },
    setTerminalCopy(state, action: PayloadAction<{ output: string; status: string }>) {
      state.terminalCopyOutput = action.payload.output;
      state.terminalCopyStatus = action.payload.status;
    },
    setTerminalCopyStatus(state, action: PayloadAction<string>) {
      state.terminalCopyStatus = action.payload;
    },
    setTerminalCopyOutput(state, action: PayloadAction<string>) {
      state.terminalCopyOutput = action.payload;
    },
    setRetrySelection(state, action: PayloadAction<UISelection | null>) {
      state.retrySelection = action.payload;
    },
    hideLockOverlay(state, action: PayloadAction<number>) {
      state.hiddenLockSessions[action.payload] = true;
    },
    clearHiddenLockOverlay(state, action: PayloadAction<number>) {
      Reflect.deleteProperty(state.hiddenLockSessions, action.payload);
    },
    setDebugCopyStatus(state, action: PayloadAction<string>) {
      state.debugCopyStatus = action.payload;
    },
  },
});

export const {
  setTerminalMessage,
  clearTerminalStatus,
  setTerminalCopy,
  setTerminalCopyStatus,
  setTerminalCopyOutput,
  setRetrySelection,
  hideLockOverlay,
  clearHiddenLockOverlay,
  setDebugCopyStatus,
} = terminalStatusSlice.actions;
export default terminalStatusSlice.reducer;

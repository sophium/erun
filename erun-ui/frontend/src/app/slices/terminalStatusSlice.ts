import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { TerminalStatusAction, TerminalStatusKind } from '../state';

export interface TerminalStatusState {
  terminalMessage: string;
  terminalStatusKind: TerminalStatusKind;
  terminalStatusDetail: string;
  terminalStatusAction: TerminalStatusAction;
  terminalBusy: boolean;
  terminalCopyOutput: string;
  terminalCopyStatus: string;
}

const initialState: TerminalStatusState = {
  terminalMessage: '',
  terminalStatusKind: 'info',
  terminalStatusDetail: '',
  terminalStatusAction: '',
  terminalBusy: false,
  terminalCopyOutput: '',
  terminalCopyStatus: '',
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
  },
});

export const {
  setTerminalMessage,
  clearTerminalStatus,
  setTerminalCopy,
  setTerminalCopyStatus,
  setTerminalCopyOutput,
} = terminalStatusSlice.actions;
export default terminalStatusSlice.reducer;

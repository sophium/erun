import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { TerminalTab } from '../state';

export interface TerminalState {
  sessionId: number;
  tabsByEnv: Record<string, TerminalTab[]>;
  selectedSessionByEnv: Record<string, number>;
  debugOutput: string;
  pendingDebugHeader: string;
}

const initialState: TerminalState = {
  sessionId: 0,
  tabsByEnv: {},
  selectedSessionByEnv: {},
  debugOutput: '',
  pendingDebugHeader: '',
};

export const terminalSlice = createSlice({
  name: 'terminal',
  initialState,
  reducers: {
    setSessionId(state, action: PayloadAction<number>) {
      state.sessionId = action.payload;
    },
    setTabsForEnv(state, action: PayloadAction<{ key: string; tabs: TerminalTab[] }>) {
      const { key, tabs } = action.payload;
      if (tabs.length === 0) {
        Reflect.deleteProperty(state.tabsByEnv, key);
      } else {
        state.tabsByEnv[key] = tabs;
      }
    },
    clearTabsForEnv(state, action: PayloadAction<string>) {
      Reflect.deleteProperty(state.tabsByEnv, action.payload);
    },
    setSelectedSessionForEnv(state, action: PayloadAction<{ key: string; sessionId: number }>) {
      const { key, sessionId } = action.payload;
      state.selectedSessionByEnv[key] = sessionId;
    },
    clearSelectedSessionForEnv(state, action: PayloadAction<string>) {
      Reflect.deleteProperty(state.selectedSessionByEnv, action.payload);
    },
    setDebugOutput(state, action: PayloadAction<string>) {
      state.debugOutput = action.payload;
    },
    setPendingDebugHeader(state, action: PayloadAction<string>) {
      state.pendingDebugHeader = action.payload;
    },
    setAll(_state, action: PayloadAction<TerminalState>) {
      return action.payload;
    },
  },
});

export const {
  setSessionId,
  setTabsForEnv,
  clearTabsForEnv,
  setSelectedSessionForEnv,
  clearSelectedSessionForEnv,
  setDebugOutput,
  setPendingDebugHeader,
  setAll: setTerminalAll,
} = terminalSlice.actions;
export default terminalSlice.reducer;

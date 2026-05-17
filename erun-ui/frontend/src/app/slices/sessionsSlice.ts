import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { DebugOpenFilter, DebugSessionMode } from '../model';
import type { UISelection } from '@/types';

// sessionsSlice owns per-session metadata: which sessions are open / sshd /
// doctor / cloud-init, which exit reasons/outputs have been recorded, the
// debug filter and mode, and the debug header buffer.
//
// The raw output buffers (Uint8Array[]) and the display-filtered
// TerminalWriteData[] arrays still live on TerminalSessionRegistry as
// instance Maps — they are large, churn frequently, and were left out of
// Redux deliberately for perf. Everything else metadata-shaped is here.

export interface SessionsState {
  selectionToSessionId: Record<string, number>;
  openSelections: Record<number, UISelection>;
  sshdInitSelections: Record<number, UISelection>;
  doctorSelections: Record<number, UISelection>;
  cloudInitSessions: Record<number, true>;
  exitReasons: Record<number, string>;
  exitOutputs: Record<number, string>;
  debugFilters: Record<number, DebugOpenFilter>;
  debugModes: Record<number, DebugSessionMode>;
  debugBuffers: Record<number, string>;
}

const initialState: SessionsState = {
  selectionToSessionId: {},
  openSelections: {},
  sshdInitSelections: {},
  doctorSelections: {},
  cloudInitSessions: {},
  exitReasons: {},
  exitOutputs: {},
  debugFilters: {},
  debugModes: {},
  debugBuffers: {},
};

export const sessionsSlice = createSlice({
  name: 'sessions',
  initialState,
  reducers: {
    trackOpenSession(
      state,
      action: PayloadAction<{ key: string; sessionId: number; selection: UISelection }>,
    ) {
      const { key, sessionId, selection } = action.payload;
      state.selectionToSessionId[key] = sessionId;
      state.openSelections[sessionId] = selection;
    },
    trackSSHDInitSession(
      state,
      action: PayloadAction<{ sessionId: number; selection: UISelection }>,
    ) {
      state.sshdInitSelections[action.payload.sessionId] = action.payload.selection;
    },
    trackDoctorSession(
      state,
      action: PayloadAction<{ sessionId: number; selection: UISelection }>,
    ) {
      state.doctorSelections[action.payload.sessionId] = action.payload.selection;
    },
    trackCloudInitSession(state, action: PayloadAction<number>) {
      state.cloudInitSessions[action.payload] = true;
    },
    recordExitReason(state, action: PayloadAction<{ sessionId: number; reason: string }>) {
      state.exitReasons[action.payload.sessionId] = action.payload.reason;
    },
    recordExitOutput(state, action: PayloadAction<{ sessionId: number; output: string }>) {
      state.exitOutputs[action.payload.sessionId] = action.payload.output;
    },
    setDebugFilter(state, action: PayloadAction<{ sessionId: number; filter: DebugOpenFilter }>) {
      state.debugFilters[action.payload.sessionId] = action.payload.filter;
    },
    clearDebugFilter(state, action: PayloadAction<number>) {
      delete state.debugFilters[action.payload];
    },
    registerDebugSession(
      state,
      action: PayloadAction<{ sessionId: number; selection: UISelection; mode: DebugSessionMode }>,
    ) {
      if (!action.payload.selection.debug) {
        return;
      }
      state.debugModes[action.payload.sessionId] = action.payload.mode;
    },
    setSessionDebug(state, action: PayloadAction<{ sessionId: number; value: string }>) {
      if (!action.payload.value) {
        delete state.debugBuffers[action.payload.sessionId];
        return;
      }
      state.debugBuffers[action.payload.sessionId] = action.payload.value;
    },
    clearSessionDebug(state, action: PayloadAction<number>) {
      delete state.debugBuffers[action.payload];
    },
    // takeExitSelections clears all selection-tracking entries for the
    // session in one atomic action. The caller reads the values out of
    // state before dispatching this.
    takeExitSelections(state, action: PayloadAction<{ sessionId: number; selectionKey: string | null }>) {
      const { sessionId, selectionKey } = action.payload;
      delete state.sshdInitSelections[sessionId];
      delete state.doctorSelections[sessionId];
      delete state.openSelections[sessionId];
      delete state.cloudInitSessions[sessionId];
      delete state.debugModes[sessionId];
      delete state.debugBuffers[sessionId];
      if (selectionKey !== null) {
        delete state.selectionToSessionId[selectionKey];
      }
    },
  },
});

export const {
  trackOpenSession,
  trackSSHDInitSession,
  trackDoctorSession,
  trackCloudInitSession,
  recordExitReason,
  recordExitOutput,
  setDebugFilter,
  clearDebugFilter,
  registerDebugSession,
  setSessionDebug,
  clearSessionDebug,
  takeExitSelections,
} = sessionsSlice.actions;
export default sessionsSlice.reducer;

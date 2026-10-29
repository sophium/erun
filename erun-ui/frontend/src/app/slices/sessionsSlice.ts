import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { CloudInitProvider } from '@/app/model';
import type { UISelection } from '@/types';

// Terminal output buffers live outside Redux for perf; only metadata is here.

export interface SessionsState {
  selectionToSessionId: Record<string, number>;
  openSelections: Record<number, UISelection>;
  cloudInitSessions: Record<number, CloudInitProvider>;
  exitReasons: Record<number, string>;
  exitOutputs: Record<number, string>;
  openingByEnv: Record<string, true>;
  // closingEnvs counts in-flight deliberate closes per env (a flag would clear
  // too early on overlapping closes), so exit events don't respawn a sibling
  // default tab (see dropExitedSessionFromTabs). Whether an exit was deliberate
  // comes from terminalExitPayload.Deliberate, not from this.
  closingEnvs: Record<string, number>;
}

const initialState: SessionsState = {
  selectionToSessionId: {},
  openSelections: {},
  cloudInitSessions: {},
  exitReasons: {},
  exitOutputs: {},
  openingByEnv: {},
  closingEnvs: {},
};

export function envKey(tenant: string, environment: string): string {
  return `${tenant} ${environment}`;
}

export function envClosing(state: SessionsState, key: string): boolean {
  return (state.closingEnvs[key] ?? 0) > 0;
}

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
    trackCloudInitSession(
      state,
      action: PayloadAction<{ sessionId: number; provider: CloudInitProvider }>,
    ) {
      state.cloudInitSessions[action.payload.sessionId] = action.payload.provider;
    },
    recordExitReason(state, action: PayloadAction<{ sessionId: number; reason: string }>) {
      state.exitReasons[action.payload.sessionId] = action.payload.reason;
    },
    recordExitOutput(state, action: PayloadAction<{ sessionId: number; output: string }>) {
      state.exitOutputs[action.payload.sessionId] = action.payload.output;
    },
    takeExitSelections(
      state,
      action: PayloadAction<{ sessionId: number; selectionKey: string | null }>,
    ) {
      const { sessionId, selectionKey } = action.payload;
      Reflect.deleteProperty(state.openSelections, sessionId);
      Reflect.deleteProperty(state.cloudInitSessions, sessionId);
      if (selectionKey !== null) {
        Reflect.deleteProperty(state.selectionToSessionId, selectionKey);
      }
    },
    markEnvOpening(state, action: PayloadAction<{ tenant: string; environment: string }>) {
      state.openingByEnv[envKey(action.payload.tenant, action.payload.environment)] = true;
    },
    clearEnvOpening(state, action: PayloadAction<{ tenant: string; environment: string }>) {
      Reflect.deleteProperty(
        state.openingByEnv,
        envKey(action.payload.tenant, action.payload.environment),
      );
    },
    // Clears a stale spinner from a slow previous open the user has left.
    resetEnvOpening(state) {
      state.openingByEnv = {};
    },
    markEnvClosing(state, action: PayloadAction<string>) {
      state.closingEnvs[action.payload] = (state.closingEnvs[action.payload] ?? 0) + 1;
    },
    clearEnvClosing(state, action: PayloadAction<string>) {
      const remaining = (state.closingEnvs[action.payload] ?? 0) - 1;
      if (remaining > 0) {
        state.closingEnvs[action.payload] = remaining;
        return;
      }
      Reflect.deleteProperty(state.closingEnvs, action.payload);
    },
  },
});

export const {
  trackOpenSession,
  trackCloudInitSession,
  recordExitReason,
  recordExitOutput,
  takeExitSelections,
  markEnvOpening,
  clearEnvOpening,
  resetEnvOpening,
  markEnvClosing,
  clearEnvClosing,
} = sessionsSlice.actions;
export default sessionsSlice.reducer;

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { CloudInitProvider } from '@/app/model';
import type { UISelection } from '@/types';

// The large, frequently-churning terminal output buffers deliberately live
// outside Redux (on TerminalSessionRegistry) for perf; only metadata lives here.

export interface SessionsState {
  selectionToSessionId: Record<string, number>;
  openSelections: Record<number, UISelection>;
  cloudInitSessions: Record<number, CloudInitProvider>;
  exitReasons: Record<number, string>;
  exitOutputs: Record<number, string>;
  // Drives a per-env "opening" spinner in the sidebar, independent of the
  // user's current selection or the terminal busy overlay.
  openingByEnv: Record<string, true>;
  // Envs (selectionKey-keyed) mid-teardown from a deliberate closeEnvironment,
  // as a count of the closes currently in flight rather than a flag. Its own
  // default-tab sessions still exit asynchronously once torn down, and those
  // terminal-exit events race the thunk's own tab-clearing dispatch — see
  // dropExitedSessionFromTabs and handleTerminalExit in wailsEventThunks.ts.
  // Overlapping closes of one env (a double-triggered close) each mark and
  // clear it; a flag would let the first close to finish turn the guard off
  // while the second's sessions are still exiting, so those exits would read
  // as unexpected deaths.
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

// envClosing is the one truth test for "this env is mid-teardown right now",
// shared by every reader so a close-in-flight is never re-derived from the
// count by hand: a key at zero or absent reads as not-closing.
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
    // openSelection calls this at the top of each click so a stale spinner
    // from a slow previous open (e.g. EC2 cold start) does not linger on a row
    // the user has navigated away from. The abandoned env's StartSession still
    // runs in the background; its state mutations are dropped by the
    // isCurrentSelection gate in openSelection.
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

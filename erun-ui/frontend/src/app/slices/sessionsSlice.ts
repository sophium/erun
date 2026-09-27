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
  //
  // That count holds the env only while a close is in flight. It cannot hold it
  // once one has settled, because settling is not the same as being done: see
  // closingSessions for the part of the hold that outlives the RPC.
  closingEnvs: Record<string, number>;
  // Sessions a close set out to tear down, sessionId -> the env that close was
  // for. Adopted when the close starts, from the env's tab strip — the desktop
  // kills an env's sessions as it finds them, which is the same set its own
  // collectAndMarkClosedForSelection snapshots under a lock.
  //
  // This is the hold that survives the RPC, and it is per session on purpose.
  // The desktop's close continues past a session whose Close reports an error,
  // so an RPC that comes back an error says the walk stopped early, not that
  // nothing was torn down: whatever it had already reached is dead and its exit
  // is still on its way, and those exits have to be recognisable as this
  // close's own. The RPC cannot say which ones they are — it returns the closed
  // serials alongside the error and a rejected transport drops them — so the
  // close names them itself, before it starts.
  //
  // Each mark is consumed by its own session's exit and covers nothing else, so
  // the silence a close buys is exactly the exits it caused, one per session it
  // aimed at. That is what keeps it from being a second env-wide hold: a
  // session the close never touched, and any session opened afterwards, is free
  // to report its death the moment the close settles.
  closingSessions: Record<number, string>;
}

const initialState: SessionsState = {
  selectionToSessionId: {},
  openSelections: {},
  cloudInitSessions: {},
  exitReasons: {},
  exitOutputs: {},
  openingByEnv: {},
  closingEnvs: {},
  closingSessions: {},
};

export function envKey(tenant: string, environment: string): string {
  return `${tenant} ${environment}`;
}

// envClosing is the one truth test for "this env is mid-teardown right now",
// shared by every reader so a close-in-flight is never re-derived from the
// count by hand: a key at zero or absent reads as not-closing.
//
// This is the hold a close that has not settled yet puts on its whole env —
// its exits have not necessarily started arriving, so nothing narrower can
// speak for them. Once a close settles it is closingSessions that carries the
// hold, one session at a time, and deliberately not this: a settled close has
// no way to know how much of the teardown is still in flight, and holding the
// whole env for a guess would silence sessions it never touched.
export function envClosing(state: SessionsState, key: string): boolean {
  return (state.closingEnvs[key] ?? 0) > 0;
}

// closingSessionEnv names the env whose close owns this session's exit, or
// undefined when no close does. Ownership is per session and asked directly,
// never inferred from the tab strip: the exit it has to answer for is the one
// the close killed, and that session may already be gone from the strip.
export function closingSessionEnv(state: SessionsState, sessionId: number): string | undefined {
  return state.closingSessions[sessionId];
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
    adoptClosingSessions(state, action: PayloadAction<{ key: string; sessionIds: number[] }>) {
      const { key, sessionIds } = action.payload;
      for (const sessionId of sessionIds) {
        state.closingSessions[sessionId] = key;
      }
    },
    consumeClosingSession(state, action: PayloadAction<number>) {
      Reflect.deleteProperty(state.closingSessions, action.payload);
    },
    releaseClosingSessions(state, action: PayloadAction<string>) {
      for (const [sessionId, key] of Object.entries(state.closingSessions)) {
        if (key === action.payload) {
          Reflect.deleteProperty(state.closingSessions, Number(sessionId));
        }
      }
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
  adoptClosingSessions,
  consumeClosingSession,
  releaseClosingSessions,
} = sessionsSlice.actions;
export default sessionsSlice.reducer;

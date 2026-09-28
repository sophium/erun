import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env AI activity signals, driven passively from the Go side (the
// ai-activity Wails event); the sidebar renders a spinner on busy env rows and
// a "waiting on you" marker on rows whose AI tool reported a turn boundary,
// even after the user navigates away. The policy that flips either one lives in
// erun-ui/terminal_sessions.go recordAIActivity / applyAIActivityReport, not
// here. Keyed by the same selectionKey() as tabsByEnv, and stored as
// Record<string, true> to keep the slice state serializable.
//
// The two env-keyed maps are written together from one payload, and an entry
// must never sit in both: a session the tool reported a turn boundary for is
// not working, and one it reported a turn start for is not waiting. Applying
// both fields of every event is what keeps a stale spinner from outliving the
// report that released it.
export interface AIActivityState {
  aiBusyByEnv: Record<string, true>;
  aiAwaitingByEnv: Record<string, true>;
  // Orchestrator sessions have no tenant/environment to key by, so their latch
  // is keyed by session id. Same event, same debounce policy — only the address
  // differs, because an orchestrator row is not an env row.
  //
  // Two writers feed this map, deliberately kept as one field so they cannot
  // disagree (#1087): the ai-activity event (handleAIActivity) and
  // loadOrchestrators seeding it from each orchestrator's own `busy` snapshot
  // field (planOrchestratorBusySeed). The event is the fast path while a
  // session runs; the snapshot is what makes a fetch that lands after a
  // transition — boot, a reload, a reconnect — render the true state without
  // having witnessed that transition.
  //
  // There is deliberately no awaiting counterpart here: an orchestrator reports
  // its turn boundaries through orchestrator_activity.go, which has no
  // blocked-on-the-operator state, so nothing would ever write one.
  aiBusyBySession: Record<number, true>;
}

const initialState: AIActivityState = {
  aiBusyByEnv: {},
  aiAwaitingByEnv: {},
  aiBusyBySession: {},
};

export const aiActivitySlice = createSlice({
  name: 'aiActivity',
  initialState,
  reducers: {
    setAIBusyForEnv(state, action: PayloadAction<{ key: string; busy: boolean }>) {
      if (action.payload.busy) {
        state.aiBusyByEnv[action.payload.key] = true;
      } else {
        Reflect.deleteProperty(state.aiBusyByEnv, action.payload.key);
      }
    },
    setAIAwaitingForEnv(state, action: PayloadAction<{ key: string; awaiting: boolean }>) {
      if (action.payload.awaiting) {
        state.aiAwaitingByEnv[action.payload.key] = true;
      } else {
        Reflect.deleteProperty(state.aiAwaitingByEnv, action.payload.key);
      }
    },
    setAIBusyForSession(state, action: PayloadAction<{ sessionId: number; busy: boolean }>) {
      if (action.payload.busy) {
        state.aiBusyBySession[action.payload.sessionId] = true;
      } else {
        Reflect.deleteProperty(state.aiBusyBySession, action.payload.sessionId);
      }
    },
  },
});

export const { setAIBusyForEnv, setAIAwaitingForEnv, setAIBusyForSession } =
  aiActivitySlice.actions;
export default aiActivitySlice.reducer;

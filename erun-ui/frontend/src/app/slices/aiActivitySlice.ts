import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// An orchestrator's own turn-busy latch, driven passively from the Go side
// (the ai-activity Wails event). An orchestrator has no pod to read a
// structured AI-session status from, so it reports its own turn boundaries
// directly (erun-ui/orchestrator_activity.go) and this is how that reaches
// the sidebar spinner. An environment's AI-session state is a different,
// richer model read from envStatusSlice's activityByEnv instead — see
// UIEnvironmentActivity.
//
// Two writers feed this map, deliberately kept as one field so they cannot
// disagree (#1087): the ai-activity event (handleAIActivity) and
// loadOrchestrators seeding it from each orchestrator's own `busy` snapshot
// field (planOrchestratorBusySeed). The event is the fast path while a
// session runs; the snapshot is what makes a fetch that lands after a
// transition — boot, a reload, a reconnect — render the true state without
// having witnessed that transition.
export interface AIActivityState {
  aiBusyBySession: Record<number, true>;
}

const initialState: AIActivityState = {
  aiBusyBySession: {},
};

export const aiActivitySlice = createSlice({
  name: 'aiActivity',
  initialState,
  reducers: {
    setAIBusyForSession(state, action: PayloadAction<{ sessionId: number; busy: boolean }>) {
      if (action.payload.busy) {
        state.aiBusyBySession[action.payload.sessionId] = true;
      } else {
        Reflect.deleteProperty(state.aiBusyBySession, action.payload.sessionId);
      }
    },
    clearAIBusy(state) {
      state.aiBusyBySession = {};
    },
  },
});

export const { setAIBusyForSession, clearAIBusy } = aiActivitySlice.actions;
export default aiActivitySlice.reducer;

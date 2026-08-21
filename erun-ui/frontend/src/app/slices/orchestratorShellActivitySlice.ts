import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-orchestrator-session "background shell running" state, driven
// passively from the Go side (the orchestrator-shell-activity Wails event).
// Independent of aiActivitySlice's busy latch: a background shell can outlive
// the turn that started it, so the sidebar row needs to spin on this even
// while the orchestrator itself reads idle.
//
// Two writers feed this map, deliberately kept as one field so they cannot
// disagree (the same treatment aiActivitySlice documents): the
// orchestrator-shell-activity event (handleOrchestratorShellActivity) and
// loadOrchestrators seeding it from each orchestrator's own
// shellRunning/shellCommand/shellStartedAtUnix snapshot fields
// (planOrchestratorShellSeed). The event is the fast path while a session
// runs; the snapshot is what makes a fetch that lands after a transition —
// boot, a reload, a reconnect — render the true state without having
// witnessed that transition.
export interface OrchestratorShellActivity {
  running: boolean;
  command: string;
  startedAtUnix: number;
}

export interface OrchestratorShellActivityState {
  bySession: Record<number, OrchestratorShellActivity>;
}

const initialState: OrchestratorShellActivityState = {
  bySession: {},
};

export const orchestratorShellActivitySlice = createSlice({
  name: 'orchestratorShellActivity',
  initialState,
  reducers: {
    setShellActivityForSession(
      state,
      action: PayloadAction<{ sessionId: number; activity: OrchestratorShellActivity }>,
    ) {
      const { sessionId, activity } = action.payload;
      if (activity.running) {
        state.bySession[sessionId] = activity;
      } else {
        Reflect.deleteProperty(state.bySession, sessionId);
      }
    },
    clearOrchestratorShellActivity(state) {
      state.bySession = {};
    },
  },
});

export const { setShellActivityForSession, clearOrchestratorShellActivity } =
  orchestratorShellActivitySlice.actions;
export default orchestratorShellActivitySlice.reducer;

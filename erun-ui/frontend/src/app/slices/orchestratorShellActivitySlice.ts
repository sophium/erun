import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-orchestrator-session "background shell running" state, separate from the
// AI busy latch because a shell can outlive its turn. Written by both the event
// and loadOrchestrators' snapshot seed.
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

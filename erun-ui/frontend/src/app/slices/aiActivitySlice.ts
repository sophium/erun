import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env "AI tab is producing output" latch from the ai-activity event;
// debounce lives in erun-ui/terminal_sessions.go recordAIActivity.
export interface AIActivityState {
  aiBusyByEnv: Record<string, true>;
  // Orchestrator sessions are keyed by session id. Both the ai-activity event
  // and loadOrchestrators' busy snapshot write this one map.
  aiBusyBySession: Record<number, true>;
}

const initialState: AIActivityState = {
  aiBusyByEnv: {},
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
    setAIBusyForSession(state, action: PayloadAction<{ sessionId: number; busy: boolean }>) {
      if (action.payload.busy) {
        state.aiBusyBySession[action.payload.sessionId] = true;
      } else {
        Reflect.deleteProperty(state.aiBusyBySession, action.payload.sessionId);
      }
    },
    clearAIBusy(state) {
      state.aiBusyByEnv = {};
      state.aiBusyBySession = {};
    },
  },
});

export const { setAIBusyForEnv, setAIBusyForSession, clearAIBusy } = aiActivitySlice.actions;
export default aiActivitySlice.reducer;

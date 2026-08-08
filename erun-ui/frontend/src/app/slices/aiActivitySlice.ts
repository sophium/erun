import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env "AI tab is producing output" latch, driven passively from the
// Go side (the ai-activity Wails event); the sidebar renders a spinner on
// busy env rows even after the user navigates away. The debounce policy
// that flips it lives in erun-ui/terminal_sessions.go recordAIActivity,
// not here. Keyed by the same selectionKey() as tabsByEnv, and stored as
// Record<string, true> to keep the slice state serializable.
export interface AIActivityState {
  aiBusyByEnv: Record<string, true>;
  // Orchestrator sessions have no tenant/environment to key by, so their latch
  // is keyed by session id. Same event, same debounce policy — only the address
  // differs, because an orchestrator row is not an env row.
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

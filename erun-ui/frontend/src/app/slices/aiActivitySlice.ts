import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// Per-env "AI tab is producing output" latch, driven passively from the
// Go side (the ai-activity Wails event); the sidebar renders a spinner on
// busy env rows even after the user navigates away. The debounce policy
// that flips it lives in erun-ui/terminal_sessions.go recordAIActivity,
// not here. Keyed by the same selectionKey() as tabsByEnv, and stored as
// Record<string, true> to keep the slice state serializable.
export interface AIActivityState {
  aiBusyByEnv: Record<string, true>;
}

const initialState: AIActivityState = {
  aiBusyByEnv: {},
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
    clearAIBusy(state) {
      state.aiBusyByEnv = {};
    },
  },
});

export const { setAIBusyForEnv, clearAIBusy } = aiActivitySlice.actions;
export default aiActivitySlice.reducer;

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

// aiActivitySlice keeps the per-env "AI tab is producing output" latch
// driven by the Go-side ai-activity Wails event. The sidebar reads
// aiBusyByEnv to render a spinner on env rows whose AI session is
// actively working, even when the user has navigated away. See
// erun-ui/terminal_sessions.go: recordAIActivity for the debounce
// policy (busy on after 5 s of sustained output, off after 3 s of
// silence; the Go side also clears the latch on session close).
//
// Keyed by `${tenant}\x00${environment}` (the same selectionKey() used
// by tabsByEnv etc.). Stored as Record<string, true> so the slice
// state stays serializable.
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

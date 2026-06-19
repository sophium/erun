import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { AgentOutputEntry, UISelection } from '@/types';

// outputsDialog holds the per-env Outputs dialog state: the resolved listing of
// the runtime pod's outputs directory (the deliverables an agent produced),
// loading/error while ListAgentOutputs runs, and the feedback from the last
// download. It is read + download only; nothing mutates the pod.
export interface OutputsDialogState {
  open: boolean;
  loading: boolean;
  error: string;
  dir: string;
  entries: AgentOutputEntry[];
  selection: UISelection | null;
  // downloadingName is the entry currently being downloaded (one at a time),
  // so its row can show a spinner; empty when no download is in flight.
  downloadingName: string;
  // status is the visible result of the last download (saved path, cancelled,
  // or an error), so the operator can tell whether their click succeeded.
  status: string;
  statusError: boolean;
}

const initialState: OutputsDialogState = {
  open: false,
  loading: false,
  error: '',
  dir: '',
  entries: [],
  selection: null,
  downloadingName: '',
  status: '',
  statusError: false,
};

export const outputsDialogSlice = createSlice({
  name: 'outputsDialog',
  initialState,
  reducers: {
    openOutputsDialog(state, action: PayloadAction<UISelection>) {
      state.open = true;
      state.loading = true;
      state.error = '';
      state.entries = [];
      state.dir = '';
      state.selection = action.payload;
      state.downloadingName = '';
      state.status = '';
      state.statusError = false;
    },
    setOutputs(state, action: PayloadAction<{ dir: string; entries: AgentOutputEntry[] }>) {
      state.loading = false;
      state.error = '';
      state.dir = action.payload.dir;
      state.entries = action.payload.entries;
    },
    setOutputsError(state, action: PayloadAction<string>) {
      state.loading = false;
      state.error = action.payload;
    },
    setOutputsDownloading(state, action: PayloadAction<string>) {
      state.downloadingName = action.payload;
      if (action.payload !== '') {
        state.status = '';
        state.statusError = false;
      }
    },
    setOutputsStatus(state, action: PayloadAction<{ message: string; error: boolean }>) {
      state.downloadingName = '';
      state.status = action.payload.message;
      state.statusError = action.payload.error;
    },
    closeOutputsDialog() {
      return initialState;
    },
  },
});

export const {
  openOutputsDialog,
  setOutputs,
  setOutputsError,
  setOutputsDownloading,
  setOutputsStatus,
  closeOutputsDialog,
} = outputsDialogSlice.actions;
export default outputsDialogSlice.reducer;

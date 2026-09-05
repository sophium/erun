import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { AgentOutputEntry, UISelection } from '@/types';

// Which producer's deliverables the dialog is showing. An environment's agent
// writes them in a runtime pod; an orchestrator has no pod and writes them on
// this host. Modelled as a union rather than two nullable fields so a target
// cannot be half-set.
export type OutputsTarget =
  | { kind: 'environment'; selection: UISelection }
  | { kind: 'orchestrator'; orchestratorId: string; name: string };

// State for the Outputs dialog: the deliverables an agent produced. Read +
// download (+ run on host) only; nothing mutates the producer.
export interface OutputsDialogState {
  open: boolean;
  loading: boolean;
  error: string;
  dir: string;
  entries: AgentOutputEntry[];
  target: OutputsTarget | null;
  downloadingName: string;
  runningName: string;
  status: string;
  statusError: boolean;
}

const initialState: OutputsDialogState = {
  open: false,
  loading: false,
  error: '',
  dir: '',
  entries: [],
  target: null,
  downloadingName: '',
  runningName: '',
  status: '',
  statusError: false,
};

export const outputsDialogSlice = createSlice({
  name: 'outputsDialog',
  initialState,
  reducers: {
    openOutputsDialog(state, action: PayloadAction<OutputsTarget>) {
      state.open = true;
      state.loading = true;
      state.error = '';
      state.entries = [];
      state.dir = '';
      state.target = action.payload;
      state.downloadingName = '';
      state.runningName = '';
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
    setOutputsRunning(state, action: PayloadAction<string>) {
      state.runningName = action.payload;
      if (action.payload !== '') {
        state.status = '';
        state.statusError = false;
      }
    },
    setOutputsStatus(state, action: PayloadAction<{ message: string; error: boolean }>) {
      state.downloadingName = '';
      state.runningName = '';
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
  setOutputsRunning,
  setOutputsStatus,
  closeOutputsDialog,
} = outputsDialogSlice.actions;
export default outputsDialogSlice.reducer;

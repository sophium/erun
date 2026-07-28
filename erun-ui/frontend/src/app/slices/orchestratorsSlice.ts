import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

export interface OrchestratorEnvRef {
  tenant: string;
  environment: string;
  directory: string;
}

// OrchestratorInfo mirrors the Go orchestratorInfo JSON contract: a host-side
// cross-env AI session that links one or more remote-agent environments (each
// mirrored to a host directory) and, when running, exposes the terminal
// SessionID the pane attaches to. Transient ones (Investigate) are not persisted.
export interface OrchestratorInfo {
  id: string;
  name: string;
  environments: OrchestratorEnvRef[];
  tenants: string[];
  directories: string[];
  sessionId: number;
  status: string;
  transient: boolean;
}

export interface OrchestratorsState {
  items: OrchestratorInfo[];
  dialogOpen: boolean;
  editing: OrchestratorInfo | null;
  busy: boolean;
  error: string;
}

const initialState: OrchestratorsState = {
  items: [],
  dialogOpen: false,
  editing: null,
  busy: false,
  error: '',
};

export const orchestratorsSlice = createSlice({
  name: 'orchestrators',
  initialState,
  reducers: {
    setOrchestrators(state, action: PayloadAction<OrchestratorInfo[]>) {
      state.items = action.payload;
    },
    openOrchestratorDialog(state, action: PayloadAction<OrchestratorInfo | null>) {
      state.dialogOpen = true;
      state.editing = action.payload;
      state.error = '';
    },
    closeOrchestratorDialog(state) {
      state.dialogOpen = false;
      state.editing = null;
    },
    setOrchestratorsBusy(state, action: PayloadAction<boolean>) {
      state.busy = action.payload;
      if (action.payload) {
        state.error = '';
      }
    },
    setOrchestratorsError(state, action: PayloadAction<string>) {
      state.busy = false;
      state.error = action.payload;
    },
  },
});

export const {
  setOrchestrators,
  openOrchestratorDialog,
  closeOrchestratorDialog,
  setOrchestratorsBusy,
  setOrchestratorsError,
} = orchestratorsSlice.actions;
export default orchestratorsSlice.reducer;

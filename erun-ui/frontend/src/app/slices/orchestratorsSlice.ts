import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

export interface OrchestratorEnvRef {
  tenant: string;
  environment: string;
  directory: string;
}

// OrchestratorInfo mirrors the Go orchestratorInfo JSON contract: a host-side
// cross-env AI session that links one or more agent environments (each reviewed
// in a host directory) and, when running, exposes the terminal
// SessionID the pane attaches to. Transient ones (Investigate) are not persisted.
//
// `busy` is the snapshot half of the fix: the sidebar spinner used to be
// lit only by the ai-activity event, so a fetch that lands after the event (a
// fresh mount, a window reopen) had no way to know the true state. loadOrchestrators
// seeds aiBusyBySession from this field on every fetch — see planOrchestratorBusySeed
// — so the event and the snapshot write the same store field instead of
// competing for it.
//
// `shellRunning`/`shellCommand`/`shellStartedAtUnix` are the same treatment for
// a background shell, independent of `busy`: a shell can outlive the
// turn that started it. See planOrchestratorShellSeed.
export interface OrchestratorInfo {
  id: string;
  name: string;
  environments: OrchestratorEnvRef[];
  tenants: string[];
  directories: string[];
  sessionId: number;
  status: string;
  busy: boolean;
  // busyAtUnix is when that busy report was written. Optional because a
  // report can predate the field; the label degrades rather than inventing a
  // duration (#1343).
  busyAtUnix?: number;
  transient: boolean;
  shellRunning: boolean;
  shellCommand: string;
  shellStartedAtUnix: number;
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

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIEnvironmentActivity } from '@/uiEnvironmentActivityTypes';

export interface OrchestratorEnvRef {
  tenant: string;
  environment: string;
  directory: string;
  // activity is the environment-activity poller's last observation for this
  // env (see uiEnvironment.Activity), joined onto the link rather than
  // collected separately — the same reused shape, so this card and the
  // sidebar row for the same environment can never disagree about what
  // "busy" or "outage" means. Undefined until the poller has observed it.
  activity?: UIEnvironmentActivity;
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
  // nudgeCount/nudgeCapped/lastNudgeAtUnix mirror orchestratorSession's own
  // pacing state (orchestrator_pacing.go): how many consecutive un-answered
  // pacing nudges erun has sent this session, and whether it gave up after
  // the cap. Zero/false for a stopped orchestrator, whose pacing state does
  // not survive past its session.
  nudgeCount: number;
  nudgeCapped: boolean;
  lastNudgeAtUnix?: number;
  // restartRequired mirrors the Go side's own comparison of what this
  // orchestrator's live session was actually spawned with against what it is
  // linked to right now: true means an edit changed the scope while the
  // session was running, so it still holds tools for an environment it was
  // unlinked from and none for one newly linked — only Restart re-wires it
  // (erun#1319).
  restartRequired: boolean;
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

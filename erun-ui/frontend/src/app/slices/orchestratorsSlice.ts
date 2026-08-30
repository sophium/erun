import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { OrchestratorNotice } from '@/app/orchestratorRestore';
import type { UIEnvironmentActivity } from '@/uiEnvironmentActivityTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

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
  // usage is the environment-usage sweep's last cached reading for this env
  // (see uiEnvironment.Usage), joined the same way activity is — undefined
  // until the sweep has observed it at least once.
  usage?: UIEnvironmentUsageSnapshot;
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
  // restoreNotices is what a restore had to say about how it resolved this
  // launch's set of reopened orchestrators -- kept apart from `error` because
  // most of these are not errors: a resumed tracked conversation is the
  // mechanism working, not a failure to render through the same field as one.
  restoreNotices: OrchestratorNotice[];
}

const initialState: OrchestratorsState = {
  items: [],
  dialogOpen: false,
  editing: null,
  busy: false,
  error: '',
  restoreNotices: [],
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
      state.restoreNotices = [];
    },
    closeOrchestratorDialog(state) {
      state.dialogOpen = false;
      state.editing = null;
    },
    setOrchestratorsBusy(state, action: PayloadAction<boolean>) {
      state.busy = action.payload;
      if (action.payload) {
        state.error = '';
        state.restoreNotices = [];
      }
    },
    setOrchestratorsError(state, action: PayloadAction<string>) {
      state.busy = false;
      state.error = action.payload;
    },
    setOrchestratorRestoreNotices(state, action: PayloadAction<OrchestratorNotice[]>) {
      state.restoreNotices = action.payload;
    },
    // setEnvActivityForOrchestratorEnvs is the live half of the activity join:
    // a fetch (setOrchestrators) joins the poller's snapshot onto each env ref
    // at that instant, but nothing previously kept it current between fetches,
    // so a card could still show an outage the poller had already cleared. This
    // patches every ref across every orchestrator that names the given
    // tenant/environment, driven by the same env-activity event that already
    // updates the sidebar row (see envStatusSlice.setEnvActivityForEnv), so the
    // two never read from inputs of different ages again.
    setEnvActivityForOrchestratorEnvs(
      state,
      action: PayloadAction<{
        tenant: string;
        environment: string;
        activity: UIEnvironmentActivity;
      }>,
    ) {
      const { tenant, environment, activity } = action.payload;
      for (const orchestrator of state.items) {
        for (const ref of orchestrator.environments) {
          if (ref.tenant === tenant && ref.environment === environment) {
            ref.activity = activity;
          }
        }
      }
    },
    // setEnvUsageForOrchestratorEnvs is the usage counterpart to
    // setEnvActivityForOrchestratorEnvs: the usage sweep's own event patches
    // every ref across every orchestrator that names the given
    // tenant/environment, so the orchestrator card and the sidebar hover card
    // for the same environment read the one cached figure rather than two
    // fetches of different ages.
    setEnvUsageForOrchestratorEnvs(
      state,
      action: PayloadAction<{
        tenant: string;
        environment: string;
        usage: UIEnvironmentUsageSnapshot;
      }>,
    ) {
      const { tenant, environment, usage } = action.payload;
      for (const orchestrator of state.items) {
        for (const ref of orchestrator.environments) {
          if (ref.tenant === tenant && ref.environment === environment) {
            ref.usage = usage;
          }
        }
      }
    },
  },
});

export const {
  setOrchestrators,
  openOrchestratorDialog,
  closeOrchestratorDialog,
  setOrchestratorsBusy,
  setOrchestratorsError,
  setOrchestratorRestoreNotices,
  setEnvActivityForOrchestratorEnvs,
  setEnvUsageForOrchestratorEnvs,
} = orchestratorsSlice.actions;
export default orchestratorsSlice.reducer;

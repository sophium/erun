import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIEnvironmentActivity } from '@/uiEnvironmentActivityTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

// OrchestratorEnvRole mirrors Go config.OrchestratorEnvRole; '' is undeclared.
export type OrchestratorEnvRole = 'code' | 'build' | 'runtime' | '';

// OrchestratorAlias mirrors Go OrchestratorConfig.Alias; '' follows the machine's alias.
export type OrchestratorAlias = string;

// OrchestratorUseGateway, OrchestratorModel and OrchestratorEffort mirror Go
// OrchestratorConfig; '' means the default.
export type OrchestratorUseGateway = boolean;
export type OrchestratorModel = string;
export type OrchestratorEffort = string;

export interface OrchestratorLaunchSettings {
  useGateway: OrchestratorUseGateway;
  model: OrchestratorModel;
  effort: OrchestratorEffort;
}

export interface OrchestratorEnvRef {
  tenant: string;
  environment: string;
  directory: string;
  role: OrchestratorEnvRole;
  // activity is the poller's observation joined onto the link, shared with the sidebar row.
  activity?: UIEnvironmentActivity;
  usage?: UIEnvironmentUsageSnapshot;
}

// OrchestratorInfo mirrors the Go orchestratorInfo JSON contract. `busy` and the
// shell* fields seed the activity slices on every fetch.
export interface OrchestratorInfo {
  id: string;
  name: string;
  environments: OrchestratorEnvRef[];
  tenants: string[];
  directories: string[];
  sessionId: number;
  status: string;
  busy: boolean;
  // busyAtUnix is optional because older reports predate it.
  busyAtUnix?: number;
  transient: boolean;
  shellRunning: boolean;
  shellCommand: string;
  shellStartedAtUnix: number;
  // nudgeCount/nudgeCapped/lastNudgeAtUnix are the live pacing budget; they
  // reset whenever the session answers.
  nudgeCount: number;
  nudgeCapped: boolean;
  lastNudgeAtUnix?: number;
  // autoNudgeCount/whipCount and their timestamps are cumulative history,
  // persisted per orchestrator id across restarts.
  autoNudgeCount: number;
  lastAutoNudgeAtUnix?: number;
  whipCount: number;
  lastWhipAtUnix?: number;
  lastCappedAtUnix?: number;
  // nudgeHistoryUnreadable: the counts above are an unverified zero.
  nudgeHistoryUnreadable?: boolean;
  // pacingUnreachable: a session runs elsewhere that this desktop's pacer cannot nudge.
  pacingUnreachable?: boolean;
  // restartRequired: linked envs changed while running; only Restart re-wires the tools.
  restartRequired: boolean;
  // roleChanged offers Restart without the "tools missing" wording.
  roleChanged: boolean;
  // alias is a selection among this host's aliases, never a credential; sent
  // back unchanged on save.
  alias?: OrchestratorAlias;
  // useGateway/model/effort are absent at their defaults; sent back unchanged on save.
  useGateway?: OrchestratorUseGateway;
  model?: OrchestratorModel;
  effort?: OrchestratorEffort;
  launchSettingsChanged: boolean;
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
    // Keeps joined activity current between fetches, from the same event the sidebar row uses.
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
  setEnvActivityForOrchestratorEnvs,
  setEnvUsageForOrchestratorEnvs,
} = orchestratorsSlice.actions;
export default orchestratorsSlice.reducer;

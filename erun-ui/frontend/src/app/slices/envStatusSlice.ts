import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

// Per-env status behind the sidebar dot; an absent key means healthy. 'stopped'
// is a stopped cloud context, 'runtime-stopped' a runtime scaled to zero.
export type EnvRealStatus = 'stopped' | 'runtime-stopped' | 'failed';

const envRealStatuses: readonly string[] = ['stopped', 'runtime-stopped', 'failed'];

// EnvObservedActivity is what the environment itself reports each poll, kept
// apart from the sticky status.
export interface EnvObservedActivity {
  reachable: boolean;
  // observed separates "reports no work" from "never answered"; only the first clears a latch.
  observed: boolean;
  // outage: the forward was lost and repair failed. Can be true while reachable is false.
  outage: boolean;
  // checkFailed: a real attempt over the runtime pod (no local forward) failed.
  checkFailed: boolean;
  busy: boolean;
  detail: string;
}

export interface EnvStatusState {
  statusByEnv: Record<string, EnvRealStatus>;
  activityByEnv: Record<string, EnvObservedActivity>;
  // nodeByEnv: absent key means no erun-managed node backs the env.
  nodeByEnv: Record<string, UIEnvironmentNodeSnapshot>;
  // usageByEnv: absent key means not yet observed, not idle.
  usageByEnv: Record<string, UIEnvironmentUsageSnapshot>;
}

const initialState: EnvStatusState = {
  statusByEnv: {},
  activityByEnv: {},
  usageByEnv: {},
  nodeByEnv: {},
};

// Quiet environments carry no entry, keeping repeated quiet polls byte-identical.
function isQuietEnvironmentActivity(activity: EnvObservedActivity): boolean {
  return !activity.reachable && !activity.busy && !activity.outage && !activity.checkFailed;
}

function environmentActivityUnchanged(
  current: EnvObservedActivity | undefined,
  activity: EnvObservedActivity,
): boolean {
  return (
    current?.reachable === activity.reachable &&
    current.observed === activity.observed &&
    current.outage === activity.outage &&
    current.checkFailed === activity.checkFailed &&
    current.busy === activity.busy &&
    current.detail === activity.detail
  );
}

export const envStatusSlice = createSlice({
  name: 'envStatus',
  initialState,
  reducers: {
    setEnvStatusForEnv(state, action: PayloadAction<{ key: string; status: string }>) {
      const { key, status } = action.payload;
      if (envRealStatuses.includes(status)) {
        state.statusByEnv[key] = status as EnvRealStatus;
      } else {
        Reflect.deleteProperty(state.statusByEnv, key);
      }
    },
    setEnvActivityForEnv(
      state,
      action: PayloadAction<{ key: string; activity: EnvObservedActivity }>,
    ) {
      const { key, activity } = action.payload;
      if (isQuietEnvironmentActivity(activity)) {
        if (key in state.activityByEnv) {
          Reflect.deleteProperty(state.activityByEnv, key);
        }
        return;
      }
      if (environmentActivityUnchanged(state.activityByEnv[key], activity)) {
        return;
      }
      state.activityByEnv[key] = activity;
    },
    setEnvUsageForEnv(
      state,
      action: PayloadAction<{ key: string; usage: UIEnvironmentUsageSnapshot }>,
    ) {
      const { key, usage } = action.payload;
      state.usageByEnv[key] = usage;
    },
    setEnvNodeForEnv(
      state,
      action: PayloadAction<{ key: string; node: UIEnvironmentNodeSnapshot | undefined }>,
    ) {
      const { key, node } = action.payload;
      if (!node) {
        // Guarded so immer does not mint a new map (and re-render rows) every tick.
        if (key in state.nodeByEnv) {
          Reflect.deleteProperty(state.nodeByEnv, key);
        }
        return;
      }
      if (environmentNodeUnchanged(state.nodeByEnv[key], node)) {
        return;
      }
      state.nodeByEnv[key] = node;
    },
  },
});

function environmentNodeUnchanged(
  current: UIEnvironmentNodeSnapshot | undefined,
  node: UIEnvironmentNodeSnapshot,
): boolean {
  return (
    current?.name === node.name && current.label === node.label && current.status === node.status
  );
}

export const { setEnvActivityForEnv, setEnvNodeForEnv, setEnvStatusForEnv, setEnvUsageForEnv } =
  envStatusSlice.actions;
export default envStatusSlice.reducer;

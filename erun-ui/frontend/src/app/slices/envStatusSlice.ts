import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

// Per-env real status behind the sidebar's open dot: a row with live tabs must
// not read as "running" (green) when the env is actually stopped or its deploy
// failed — tab presence alone is not running-ness. An absent key means healthy.
//
// The two stopped kinds are distinct because their recovery is: 'stopped' is a
// stopped cloud context (started from the titlebar), 'runtime-stopped' is a
// runtime scaled to zero (woken by opening the environment). Both are stopped,
// neither is a failure.
export type EnvRealStatus = 'stopped' | 'runtime-stopped' | 'failed';

const envRealStatuses: readonly string[] = ['stopped', 'runtime-stopped', 'failed'];

// EnvObservedActivity is the other input to the same row state: what the
// environment itself reports, refreshed on every poll. Kept apart from the
// status because a status is a sticky condition the desktop set, while this is
// an observation that must be free to change back — and because it is true for
// an environment the desktop never opened, which is exactly the case a
// tab-presence check cannot see.
export interface EnvObservedActivity {
  reachable: boolean;
  // observed is the environment having answered the idle question. It is what
  // separates "asked, and it reports no work" from "never got an answer" —
  // busy is false either way, and only the first may clear a row's latch.
  observed: boolean;
  // outage is the environment having lost the forward it had — the local port
  // free, or held by something that replies to nothing — after the desktop's
  // bounded repair failed to fix it. It is what separates an environment with
  // nothing to say from one that cannot say anything, so the row has to render
  // it; note it can be true while reachable is false, which is the ordinary
  // shape after a pod replacement takes the forward with it.
  outage: boolean;
  // checkFailed is outage's counterpart for an environment with no local
  // forward: a real attempt to reach it over its own runtime pod that did not
  // come back, as opposed to an environment nobody has asked about at all —
  // see EnvActivityPayload for the full reasoning.
  checkFailed: boolean;
  busy: boolean;
  detail: string;
}

export interface EnvStatusState {
  statusByEnv: Record<string, EnvRealStatus>;
  activityByEnv: Record<string, EnvObservedActivity>;
  // usageByEnv is the environment-usage sweep's last cached reading per
  // environment (environment_usage.go), keyed the same way activityByEnv is.
  // Unlike activity, a quiet reading is not omitted: the figures themselves
  // are expected to change every sweep, so there is no "still quiet" case to
  // collapse away, and an absent key means "not yet observed" rather than
  // "idle".
  usageByEnv: Record<string, UIEnvironmentUsageSnapshot>;
}

const initialState: EnvStatusState = {
  statusByEnv: {},
  activityByEnv: {},
  usageByEnv: {},
};

// A quiet environment carries no entry, so a repeated "still quiet"
// observation must leave the slice byte-identical rather than producing a new
// state object every poll.
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
  },
});

export const { setEnvActivityForEnv, setEnvStatusForEnv, setEnvUsageForEnv } =
  envStatusSlice.actions;
export default envStatusSlice.reducer;

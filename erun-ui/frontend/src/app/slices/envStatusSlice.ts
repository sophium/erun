import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

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
  busy: boolean;
  detail: string;
}

export interface EnvStatusState {
  statusByEnv: Record<string, EnvRealStatus>;
  activityByEnv: Record<string, EnvObservedActivity>;
}

const initialState: EnvStatusState = {
  statusByEnv: {},
  activityByEnv: {},
};

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
      // A quiet environment carries no entry, so a repeated "still quiet"
      // observation must leave the slice byte-identical rather than producing a
      // new state object every poll.
      if (!activity.reachable && !activity.busy && !activity.outage) {
        if (key in state.activityByEnv) {
          Reflect.deleteProperty(state.activityByEnv, key);
        }
        return;
      }
      const current = state.activityByEnv[key];
      if (
        current?.reachable === activity.reachable &&
        current.outage === activity.outage &&
        current.busy === activity.busy &&
        current.detail === activity.detail
      ) {
        return;
      }
      state.activityByEnv[key] = activity;
    },
  },
});

export const { setEnvActivityForEnv, setEnvStatusForEnv } = envStatusSlice.actions;
export default envStatusSlice.reducer;

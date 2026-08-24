import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { AppNotification } from '../state';

// This used to be a single `notification: AppNotification | null` slot, so a
// burst of concurrent failures (e.g. several "Upgrade all" members failing
// within milliseconds of each other) silently overwrote one another — only
// the last write survived. notifications is a small FIFO queue instead: every
// failure gets its own entry, and the titlebar renders (and dismisses) the
// oldest one first.
export interface NotificationState {
  notifications: AppNotification[];
}

const initialState: NotificationState = {
  notifications: [],
};

export interface NotificationEnvMatch {
  tenant: string;
  environment: string;
  source: string;
}

// notificationQueueCapacity caps in-memory queue length. Success/info entries
// auto-dismiss quickly, so only a pile of undismissed errors/warnings could
// approach this; dropping the oldest keeps the newest failures visible rather
// than growing unbounded over a long desktop session.
const notificationQueueCapacity = 20;

export const notificationSlice = createSlice({
  name: 'notification',
  initialState,
  reducers: {
    showNotification(state, action: PayloadAction<AppNotification>) {
      state.notifications.push(action.payload);
      if (state.notifications.length > notificationQueueCapacity) {
        state.notifications.splice(0, state.notifications.length - notificationQueueCapacity);
      }
    },
    dismissNotification(state, action: PayloadAction<string>) {
      state.notifications = state.notifications.filter((n) => n.id !== action.payload);
    },
    // Lets the deploy lifecycle retire the warning it raised without clobbering
    // an unrelated toast. Empty `source` is a wildcard so a deploy start can
    // retire both the runtime-unreachable warning and a prior deploy-failed
    // error at once. Matches (and removes) every queued entry for the env, not
    // just the front of the queue.
    dismissNotificationForEnv(state, action: PayloadAction<NotificationEnvMatch>) {
      const { tenant, environment, source } = action.payload;
      state.notifications = state.notifications.filter((n) => {
        const envMatches = n.tenant === tenant && n.environment === environment;
        const sourceMatches = source === '' || n.source === source;
        return !(envMatches && sourceMatches);
      });
    },
  },
});

export const { showNotification, dismissNotification, dismissNotificationForEnv } =
  notificationSlice.actions;
export default notificationSlice.reducer;

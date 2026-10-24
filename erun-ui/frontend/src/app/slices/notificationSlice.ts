import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { NotificationFilter } from '../notificationCenter';
import type { AppNotification } from '../state';

// notifications is the session's full history; dismissing only marks an entry read.
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

// Caps history length, since dismissal never removes entries.
const notificationHistoryCapacity = 300;

export const notificationSlice = createSlice({
  name: 'notification',
  initialState,
  reducers: {
    showNotification(state, action: PayloadAction<AppNotification>) {
      state.notifications.push(action.payload);
      if (state.notifications.length > notificationHistoryCapacity) {
        state.notifications.splice(0, state.notifications.length - notificationHistoryCapacity);
      }
    },
    // `undefined` is a no-op, not "dismiss everything".
    dismissNotification(state, action: PayloadAction<string | undefined>) {
      const id = action.payload;
      if (!id) {
        return;
      }
      const entry = state.notifications.find((n) => n.id === id);
      if (entry) {
        entry.dismissed = true;
      }
    },
    dismissNotifications(state, action: PayloadAction<NotificationFilter>) {
      const filter = action.payload;
      for (const n of state.notifications) {
        if (filter === 'all' || n.kind === filter) {
          n.dismissed = true;
        }
      }
    },
    // Empty `source` matches any source for the env.
    dismissNotificationForEnv(state, action: PayloadAction<NotificationEnvMatch>) {
      const { tenant, environment, source } = action.payload;
      for (const n of state.notifications) {
        const envMatches = n.tenant === tenant && n.environment === environment;
        const sourceMatches = source === '' || n.source === source;
        if (envMatches && sourceMatches) {
          n.dismissed = true;
        }
      }
    },
  },
});

export const {
  showNotification,
  dismissNotification,
  dismissNotifications,
  dismissNotificationForEnv,
} = notificationSlice.actions;
export default notificationSlice.reducer;

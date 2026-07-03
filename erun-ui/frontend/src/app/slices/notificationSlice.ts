import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { AppNotification } from '../state';

export interface NotificationState {
  notification: AppNotification | null;
}

const initialState: NotificationState = {
  notification: null,
};

export interface NotificationEnvMatch {
  tenant: string;
  environment: string;
  source: string;
}

export const notificationSlice = createSlice({
  name: 'notification',
  initialState,
  reducers: {
    showNotification(state, action: PayloadAction<AppNotification>) {
      state.notification = action.payload;
    },
    dismissNotification(state) {
      state.notification = null;
    },
    // Lets the deploy lifecycle retire the warning it raised without clobbering
    // an unrelated toast. Empty `source` is a wildcard so a deploy start can
    // retire both the runtime-unreachable warning and a prior deploy-failed
    // error at once.
    dismissNotificationForEnv(state, action: PayloadAction<NotificationEnvMatch>) {
      const current = state.notification;
      if (!current) {
        return;
      }
      const { tenant, environment, source } = action.payload;
      const envMatches = current.tenant === tenant && current.environment === environment;
      const sourceMatches = source === '' || current.source === source;
      if (envMatches && sourceMatches) {
        state.notification = null;
      }
    },
  },
});

export const { showNotification, dismissNotification, dismissNotificationForEnv } =
  notificationSlice.actions;
export default notificationSlice.reducer;

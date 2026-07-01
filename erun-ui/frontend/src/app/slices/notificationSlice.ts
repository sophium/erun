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
    // dismissNotificationForEnv clears the current notification only when its
    // source/tenant/environment all match — so the deploy lifecycle can retire
    // the runtime-unreachable warning it raised without touching an unrelated
    // toast (issue #713).
    dismissNotificationForEnv(state, action: PayloadAction<NotificationEnvMatch>) {
      const current = state.notification;
      if (!current) {
        return;
      }
      const { tenant, environment, source } = action.payload;
      if (
        current.source === source &&
        current.tenant === tenant &&
        current.environment === environment
      ) {
        state.notification = null;
      }
    },
  },
});

export const { showNotification, dismissNotification, dismissNotificationForEnv } =
  notificationSlice.actions;
export default notificationSlice.reducer;

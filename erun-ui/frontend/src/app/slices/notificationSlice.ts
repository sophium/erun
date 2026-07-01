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
    // dismissNotificationForEnv clears the current notification when it targets
    // this env, so the deploy lifecycle can retire the warning it raised without
    // touching an unrelated toast (issue #713). An empty `source` matches any
    // env-scoped notification (a deploy starting retires both the
    // runtime-unreachable warning and a prior deploy-failed error); a non-empty
    // `source` clears only that kind.
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

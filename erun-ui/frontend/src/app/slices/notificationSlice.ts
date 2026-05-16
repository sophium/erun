import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { AppNotification } from '../state';

export interface NotificationState {
  notification: AppNotification | null;
}

const initialState: NotificationState = {
  notification: null,
};

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
    setAll(_state, action: PayloadAction<NotificationState>) {
      return action.payload;
    },
  },
});

export const {
  showNotification,
  dismissNotification,
  setAll: setNotificationAll,
} = notificationSlice.actions;
export default notificationSlice.reducer;

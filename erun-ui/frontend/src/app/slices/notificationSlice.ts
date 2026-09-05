import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { NotificationFilter } from '../notificationCenter';
import type { AppNotification } from '../state';

// This used to be a single `notification: AppNotification | null` slot, so a
// burst of concurrent failures (e.g. several "Upgrade all" members failing
// within milliseconds of each other) silently overwrote one another — only
// the last write survived. notifications is now the session's full message
// history (the message centre's own contract): every notification gets its
// own entry and is *retained* after it dismisses (dismissed just flips a flag),
// so the message centre dialog can list a session's whole history rather than
// only whatever is still unread.
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

// notificationHistoryCapacity caps in-memory history length. Dismissal no
// longer removes an entry (it stays for the message centre dialog), so this
// is the only thing bounding growth over a long desktop session; dropping the
// oldest keeps the most recent history rather than growing unbounded.
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
    // Marks an entry read rather than deleting it -- see NotificationState's
    // own doc comment. `undefined` (the titlebar dismiss button's "whatever is
    // currently shown" case) is a no-op rather than dismissing everything.
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
    // dismissNotifications is the bulk form of dismissNotification: the
    // message centre dialog's per-class and all-classes "Mark read" actions
    // both go through this one reducer, scoped by filter ('all' marks every
    // kind). Semantics stay identical to the single-message path -- marks
    // read, never removes.
    dismissNotifications(state, action: PayloadAction<NotificationFilter>) {
      const filter = action.payload;
      for (const n of state.notifications) {
        if (filter === 'all' || n.kind === filter) {
          n.dismissed = true;
        }
      }
    },
    // Lets the deploy lifecycle retire the warning it raised without clobbering
    // an unrelated toast. Empty `source` is a wildcard so a deploy start can
    // retire both the runtime-unreachable warning and a prior deploy-failed
    // error at once. Marks (rather than removes) every matching entry for the
    // env, not just the front of the queue.
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

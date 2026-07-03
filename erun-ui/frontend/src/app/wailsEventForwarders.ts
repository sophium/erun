import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from './activityQueueState';
import { wailsApi } from './api/wailsApi';
import type { AppNotificationClearPayload } from './model';
import { setActivityLock, upsertActivityEntry } from './slices/activitySlice';
import { dismissNotificationForEnv } from './slices/notificationSlice';
import type { AppDispatch } from './store';

// attachWailsEventForwarders bridges Go-side runtime events into Redux. The
// EventsOn subscriptions live for the page's lifetime so we never tear them
// down. Earlier wiring used createListenerMiddleware, but its abort signal
// fires the moment the async effect resolves — that immediately unsubscribed
// every registered callback, so activity entries never reached the store.
export function attachWailsEventForwarders(dispatch: AppDispatch): void {
  EventsOn('activity:state', (entry: ActivityQueueEntry) => {
    dispatch(upsertActivityEntry(entry));
  });
  EventsOn('activity:lock', (event: ActivityLockEvent) => {
    dispatch(setActivityLock(event));
  });
  // Retire an env-tagged notification when the state it described has moved on:
  // the runtime-unreachable warning is cleared once a deploy for
  // its env starts or the runtime becomes reachable. The reducer clears it only
  // when source/tenant/environment all match, so an unrelated toast is untouched.
  EventsOn('app-notification-clear', (event: AppNotificationClearPayload) => {
    dispatch(
      dismissNotificationForEnv({
        tenant: event.tenant ?? '',
        environment: event.environment ?? '',
        source: event.source ?? '',
      }),
    );
  });
  EventsOn('environments-changed', () => {
    dispatch(wailsApi.util.invalidateTags(['AppState']));
  });
}

import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from './activityQueueState';
import { wailsApi } from './api/wailsApi';
import type { AppNotificationClearPayload } from './model';
import { setActivityLock, upsertActivityEntry } from './slices/activitySlice';
import { dismissNotificationForEnv } from './slices/notificationSlice';
import type { AppDispatch } from './store';

// attachWailsEventForwarders bridges Go runtime events into Redux with
// page-lifetime subscriptions we never tear down. createListenerMiddleware is
// unusable here: its abort signal unsubscribes every callback the moment the
// async effect resolves, so activity entries never reach the store.
export function attachWailsEventForwarders(dispatch: AppDispatch): void {
  EventsOn('activity:state', (entry: ActivityQueueEntry) => {
    dispatch(upsertActivityEntry(entry));
  });
  EventsOn('activity:lock', (event: ActivityLockEvent) => {
    dispatch(setActivityLock(event));
  });
  // Retire an env-tagged notification once the state it described has moved on:
  // the runtime-unreachable warning clears when a deploy for its env starts or
  // the runtime becomes reachable.
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

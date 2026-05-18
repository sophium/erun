import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from './activityQueueState';
import { wailsApi } from './api/wailsApi';
import { setActivityLock, upsertActivityEntry } from './slices/activitySlice';
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
  EventsOn('environments-changed', () => {
    dispatch(wailsApi.util.invalidateTags(['AppState']));
  });
}

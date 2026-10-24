import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from './activityQueueState';
import type { AppNotificationClearPayload } from './model';
import { setActivityLock, upsertActivityEntry } from './slices/activitySlice';
import { openCloseGate } from './slices/closeGateSlice';
import { dismissNotificationForEnv } from './slices/notificationSlice';
import type { AppDispatch } from './store';
import { handleEventsDropped } from './wailsEventThunks';

// The event only fires when blocked is true.
interface AppCloseGatePayload {
  blocked: boolean;
  running?: ActivityQueueEntry[];
}

// createListenerMiddleware cannot be used: its abort signal unsubscribes when
// the effect resolves.
export function attachWailsEventForwarders(dispatch: AppDispatch): void {
  EventsOn('activity:state', (entry: ActivityQueueEntry) => {
    dispatch(upsertActivityEntry(entry));
  });
  EventsOn('activity:lock', (event: ActivityLockEvent) => {
    dispatch(setActivityLock(event));
  });
  EventsOn('app-notification-clear', (event: AppNotificationClearPayload) => {
    dispatch(
      dismissNotificationForEnv({
        tenant: event.tenant ?? '',
        environment: event.environment ?? '',
        source: event.source ?? '',
      }),
    );
  });
  EventsOn('app-close-gate', (gate: AppCloseGatePayload) => {
    dispatch(openCloseGate(gate.running ?? []));
  });
  EventsOn('erun:events-dropped', (missed: number) => {
    void dispatch(handleEventsDropped(missed));
  });
}

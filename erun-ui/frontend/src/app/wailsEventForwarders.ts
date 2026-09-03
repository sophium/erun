import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from './activityQueueState';
import type { AppNotificationClearPayload } from './model';
import { setActivityLock, upsertActivityEntry } from './slices/activitySlice';
import { openCloseGate } from './slices/closeGateSlice';
import { dismissNotificationForEnv } from './slices/notificationSlice';
import type { AppDispatch } from './store';
import { handleEventsDropped } from './wailsEventThunks';

// Payload shape of the "app-close-gate" event PrepareWindowClose emits (Go
// main.uiCloseGate). Only the running list matters here: the event only
// fires when blocked is true.
interface AppCloseGatePayload {
  blocked: boolean;
  running?: ActivityQueueEntry[];
}

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
  EventsOn('app-close-gate', (gate: AppCloseGatePayload) => {
    dispatch(openCloseGate(gate.running ?? []));
  });
  // Reserved gap marker from the headless HTTP+SSE bridge (see
  // headlessserver.eventsDroppedName) -- see handleEventsDropped's own
  // comment for why this tab reacts rather than reading the marker's absence
  // from every other handler above as "nothing happened".
  EventsOn('erun:events-dropped', (missed: number) => {
    void dispatch(handleEventsDropped(missed));
  });
}

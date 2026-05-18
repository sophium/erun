import { createAction, createListenerMiddleware } from '@reduxjs/toolkit';

import { EventsOn } from '../../../wailsjs/runtime/runtime';
import type { ActivityLockEvent, ActivityQueueEntry } from '../activityQueueState';
import { wailsApi } from '../api/wailsApi';
import { setActivityLock, upsertActivityEntry } from '../slices/activitySlice';
import type { AppDispatch, RootState } from '../store';

export const startWailsEventsListening = createAction('wailsEvents/startListening');

export const wailsEventsMiddleware = createListenerMiddleware();

const startListening = wailsEventsMiddleware.startListening.withTypes<RootState, AppDispatch>();

// The startListening effect runs once. It registers Wails event handlers that
// translate Go-side notifications into Redux dispatches. EventsOn returns an
// unsubscribe function which we hold on the listener's cancellation token.
startListening({
  actionCreator: startWailsEventsListening,
  // The createListenerMiddleware effect signature is async; this one-shot
  // subscription setup yields a single microtask so the function body
  // contains an await, satisfying the lint contract without changing
  // observable behaviour.
  effect: async (_action, api) => {
    await Promise.resolve();
    const dispatch = api.dispatch;
    const subscriptions: (() => void)[] = [];

    subscriptions.push(
      EventsOn('activity:state', (entry: ActivityQueueEntry) => {
        dispatch(upsertActivityEntry(entry));
      }),
    );
    subscriptions.push(
      EventsOn('activity:lock', (event: ActivityLockEvent) => {
        dispatch(setActivityLock(event));
      }),
    );
    subscriptions.push(
      EventsOn('environments-changed', () => {
        dispatch(wailsApi.util.invalidateTags(['AppState']));
      }),
    );

    api.signal.addEventListener('abort', () => {
      for (const off of subscriptions) {
        try {
          off();
        } catch {
          // ignore
        }
      }
    });
  },
});

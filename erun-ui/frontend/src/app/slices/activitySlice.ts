import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { ActivityLockEvent, ActivityQueueEntry } from '../activityQueueState';

export interface ActivityState {
  entries: ActivityQueueEntry[];
  locksBySession: Record<number, ActivityLockEvent>;
}

const initialState: ActivityState = {
  entries: [],
  locksBySession: {},
};

function sortEntries(entries: ActivityQueueEntry[]): ActivityQueueEntry[] {
  const copy = entries.slice();
  copy.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
  return copy;
}

export const activitySlice = createSlice({
  name: 'activity',
  initialState,
  reducers: {
    setActivityEntries(state, action: PayloadAction<ActivityQueueEntry[]>) {
      state.entries = sortEntries(action.payload);
    },
    upsertActivityEntry(state, action: PayloadAction<ActivityQueueEntry>) {
      const entry = action.payload;
      const idx = state.entries.findIndex((existing) => existing.id === entry.id);
      if (idx === -1) {
        state.entries = sortEntries([entry, ...state.entries]);
      } else {
        state.entries[idx] = entry;
        state.entries = sortEntries(state.entries);
      }
    },
    removeActivityEntry(state, action: PayloadAction<string>) {
      state.entries = state.entries.filter((entry) => entry.id !== action.payload);
    },
    removeActivityEntriesForSession(state, action: PayloadAction<number>) {
      const sessionString = String(action.payload);
      state.entries = state.entries.filter((entry) => entry.sessionId !== sessionString);
    },
    setActivityLock(state, action: PayloadAction<ActivityLockEvent>) {
      const event = action.payload;
      if (event.locked) {
        state.locksBySession[event.sessionId] = event;
      } else {
        Reflect.deleteProperty(state.locksBySession, event.sessionId);
      }
    },
  },
});

export const {
  setActivityEntries,
  upsertActivityEntry,
  removeActivityEntry,
  removeActivityEntriesForSession,
  setActivityLock,
} = activitySlice.actions;
export default activitySlice.reducer;

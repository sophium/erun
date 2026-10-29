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

// lastUpdatedKey compares the Go RFC3339Nano stamp at full precision
// (Date.parse floors to ms), padding the fraction since Go trims trailing
// zeros; null for any other shape.
const RFC3339_UTC = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?Z$/;

function lastUpdatedKey(entry: ActivityQueueEntry): string | null {
  const [, second, fraction] = RFC3339_UTC.exec(entry.lastUpdated) ?? [];
  if (second === undefined) {
    return null;
  }
  return `${second}.${(fraction ?? '').padEnd(9, '0').slice(0, 9)}`;
}

// lastUpdatedMs falls back to whole milliseconds; NaN means "cannot tell".
function lastUpdatedMs(entry: ActivityQueueEntry): number {
  return Date.parse(entry.lastUpdated);
}

export const activitySlice = createSlice({
  name: 'activity',
  initialState,
  reducers: {
    // Keeps synthetic 'invite-approval' entries ListDeploys never returns, and
    // per id lets the newer write win: the snapshot may predate a stream update,
    // and a finished entry gets no later emit to correct it.
    setActivityEntries(state, action: PayloadAction<ActivityQueueEntry[]>) {
      const incomingIds = new Set(action.payload.map((entry) => entry.id));
      const survivingInviteApprovals = state.entries.filter(
        (entry) => entry.origin === 'invite-approval' && !incomingIds.has(entry.id),
      );
      const currentById = new Map(state.entries.map((entry) => [entry.id, entry]));
      const reconciled = action.payload.map((entry) => {
        const current = currentById.get(entry.id);
        if (current === undefined) {
          return entry;
        }
        const currentKey = lastUpdatedKey(current);
        const incomingKey = lastUpdatedKey(entry);
        if (currentKey !== null && incomingKey !== null) {
          // On a tie the payload wins.
          return currentKey > incomingKey ? current : entry;
        }
        const currentMs = lastUpdatedMs(current);
        const incomingMs = lastUpdatedMs(entry);
        if (Number.isNaN(currentMs) || Number.isNaN(incomingMs)) {
          return entry;
        }
        return currentMs > incomingMs ? current : entry;
      });
      state.entries = sortEntries([...reconciled, ...survivingInviteApprovals]);
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

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
    // setActivityEntries reconciles the cluster/host-observed list
    // ListDeploys returns -- a wholesale replace would otherwise wipe any
    // synthetic 'invite-approval' entry (pushInviteApprovalActivityEntry)
    // every time this refetches, since ListDeploys never carries one back.
    // Any invite-approval entry not already present in the fresh payload
    // (by id) survives the sync, so dismissing an unrelated deploy entry
    // elsewhere in the drawer can't silently drop the one place the invite
    // accept link is shown.
    setActivityEntries(state, action: PayloadAction<ActivityQueueEntry[]>) {
      const incomingIds = new Set(action.payload.map((entry) => entry.id));
      const survivingInviteApprovals = state.entries.filter(
        (entry) => entry.origin === 'invite-approval' && !incomingIds.has(entry.id),
      );
      state.entries = sortEntries([...action.payload, ...survivingInviteApprovals]);
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

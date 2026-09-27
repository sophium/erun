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

// lastUpdatedMs reads the store's own write time for an entry. The store
// stamps it under the same lock that takes the ListDeploys snapshot, so it
// orders the two writers against each other: of two copies of one id, the
// higher stamp is always the later write. A missing or unparsable stamp
// yields NaN, which reads as "cannot tell" rather than as an ordering.
function lastUpdatedMs(entry: ActivityQueueEntry): number {
  return Date.parse(entry.lastUpdated);
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
    //
    // For an id this reducer already holds, the payload is a snapshot the Go
    // store took at request time and React applies later, while the
    // 'activity:state' stream (upsertActivityEntry) lands the moment the store
    // mutates. A wholesale replace therefore applies the older of the two
    // whenever a mutation lands in between -- and a terminal entry has no
    // later emit to correct it, since finishing removes it from the store's
    // active set and stops its container poller. The card would then read
    // 'running' for good, putting the sidebar row, the launcher badge, and the
    // drawer's "now" section permanently wrong. So per id the newer write
    // wins; ids the payload does not carry keep today's replace semantics,
    // which is what propagates a dismissal made outside this window.
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

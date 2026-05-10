import * as React from 'react';

import { DismissDeploy, ForceDismissActivity, ListDeploys } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export type ActivityQueueStatus = 'running' | 'succeeded' | 'failed' | 'skipped';

export type ActivityQueueContainerStatus = {
  name: string;
  image: string;
  phase: string;
  ready: boolean;
  restarts: number;
  reason?: string;
  message?: string;
};

export type ActivityQueueEntry = {
  id: string;
  command: string;
  tenant: string;
  environment: string;
  version?: string;
  release?: string;
  namespace?: string;
  kubernetesContext?: string;
  component?: string;
  image?: string;
  summary?: string;
  status: ActivityQueueStatus;
  startedAt: string;
  endedAt?: string;
  lastUpdated: string;
  containers?: ActivityQueueContainerStatus[];
  error?: string;
};

export type ActivityLockEvent = {
  sessionId: number;
  tenant: string;
  environment: string;
  locked: boolean;
  deployId?: string;
  reason?: string;
  deployTarget?: string;
};

const activityStateEventName = 'activity:state';
const activityLockEventName = 'activity:lock';

// useActivityQueue subscribes to the backend activity:state stream and
// exposes a stable, sorted snapshot to React. Initial state is fetched
// once via ListDeploys; subsequent updates are merged in place from
// event payloads so the queue reflects backend transitions without
// polling.
//
// dismiss removes a finished entry; forceDismiss removes ANY entry
// (including an active one). The latter is for stuck entries the
// watcher cannot finalize on its own — see App.ForceDismissActivity.
export function useActivityQueue(): {
  entries: ActivityQueueEntry[];
  dismiss: (id: string) => Promise<void>;
  forceDismiss: (id: string) => Promise<void>;
} {
  const [entries, setEntries] = React.useState<ActivityQueueEntry[]>([]);

  React.useEffect(() => {
    let cancelled = false;
    void ListDeploys().then((initial) => {
      if (cancelled) return;
      setEntries(sortActivityEntries((initial as ActivityQueueEntry[]) ?? []));
    });
    const off = EventsOn(activityStateEventName, (entry: ActivityQueueEntry) => {
      setEntries((prev) => mergeActivityEntry(prev, entry));
    });
    return () => {
      cancelled = true;
      off?.();
    };
  }, []);

  const dismiss = React.useCallback(async (id: string): Promise<void> => {
    const ok = await DismissDeploy(id);
    if (ok) {
      setEntries((prev) => prev.filter((entry) => entry.id !== id));
    }
  }, []);

  const forceDismiss = React.useCallback(async (id: string): Promise<void> => {
    const ok = await ForceDismissActivity(id);
    if (ok) {
      setEntries((prev) => prev.filter((entry) => entry.id !== id));
    }
  }, []);

  return { entries, dismiss, forceDismiss };
}

// useTerminalActivityLockState exposes the live map of session lock
// states keyed by terminal sessionId. Frontend renders a lock overlay
// on any terminal whose id is present in the map.
export function useTerminalActivityLockState(): Map<number, ActivityLockEvent> {
  const [locks, setLocks] = React.useState<Map<number, ActivityLockEvent>>(() => new Map());

  React.useEffect(() => {
    const off = EventsOn(activityLockEventName, (event: ActivityLockEvent) => {
      setLocks((prev) => {
        const next = new Map(prev);
        if (event.locked) {
          next.set(event.sessionId, event);
        } else {
          next.delete(event.sessionId);
        }
        return next;
      });
    });
    return () => off?.();
  }, []);

  return locks;
}

function mergeActivityEntry(prev: ActivityQueueEntry[], entry: ActivityQueueEntry): ActivityQueueEntry[] {
  const idx = prev.findIndex((existing) => existing.id === entry.id);
  if (idx === -1) {
    return sortActivityEntries([entry, ...prev]);
  }
  if (activityEntriesShallowEqual(prev[idx], entry)) {
    return prev;
  }
  const next = prev.slice();
  next[idx] = entry;
  return sortActivityEntries(next);
}

// activityEntriesShallowEqual returns true when the rendered representation
// of two entries is identical. Used to short-circuit React state updates
// when the watcher emits redundant events (which happens often: the pod
// poller emits container snapshots even when nothing changed). Cheap and
// stable comparison reduces re-renders that caused visible flicker.
function activityEntriesShallowEqual(a: ActivityQueueEntry, b: ActivityQueueEntry): boolean {
  if (a === b) return true;
  if (
    a.id !== b.id ||
    a.command !== b.command ||
    a.status !== b.status ||
    a.startedAt !== b.startedAt ||
    a.endedAt !== b.endedAt ||
    a.error !== b.error ||
    a.lastUpdated !== b.lastUpdated
  ) {
    return false;
  }
  return containersEqual(a.containers, b.containers);
}

function containersEqual(a: ActivityQueueContainerStatus[] | undefined, b: ActivityQueueContainerStatus[] | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return !a && !b;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    const x = a[i];
    const y = b[i];
    if (
      x.name !== y.name ||
      x.image !== y.image ||
      x.phase !== y.phase ||
      x.ready !== y.ready ||
      x.restarts !== y.restarts ||
      x.reason !== y.reason ||
      x.message !== y.message
    ) {
      return false;
    }
  }
  return true;
}

function sortActivityEntries(entries: ActivityQueueEntry[]): ActivityQueueEntry[] {
  const copy = entries.slice();
  copy.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
  return copy;
}

// activeActivityForSelection finds the first active entry that targets
// the given (tenant, environment). Used by the deploy button gate.
export function activeActivityForSelection(entries: ActivityQueueEntry[], tenant: string, environment: string): ActivityQueueEntry | null {
  return entries.find((entry) => entry.status === 'running' && entry.tenant === tenant && entry.environment === environment) ?? null;
}

// formatElapsed renders an ISO start timestamp as a humanized "1m12s"
// string using the supplied "now" (so callers can drive consistent
// ticks via state without React.useEffect each second). The output is
// always 6 chars wide via space-padding so the right-aligned column
// doesn't shift width on tick (was a flicker source).
export function formatElapsed(startedAt: string, now: number = Date.now()): string {
  const start = Date.parse(startedAt);
  if (Number.isNaN(start)) {
    return '';
  }
  const seconds = Math.max(0, Math.floor((now - start) / 1000));
  let raw: string;
  if (seconds < 60) {
    raw = `${seconds}s`;
  } else {
    const minutes = Math.floor(seconds / 60);
    const remSeconds = seconds % 60;
    if (minutes < 60) {
      raw = `${minutes}m${remSeconds}s`;
    } else {
      const hours = Math.floor(minutes / 60);
      const remMinutes = minutes % 60;
      raw = `${hours}h${remMinutes}m`;
    }
  }
  return raw.padStart(6, ' ');
}

import * as React from 'react';

import { DismissDeploy, ListDeploys } from '../../wailsjs/go/main/App';
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
  tenant: string;
  environment: string;
  version?: string;
  release: string;
  namespace: string;
  kubernetesContext: string;
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

const deployStateEvent = 'activity:state';
const deployLockEvent = 'activity:lock';

// useActivityQueue subscribes to the backend deploy:state stream and exposes a
// stable, sorted snapshot to React. Initial state is fetched once via
// ListDeploys; subsequent updates are merged in place from event payloads so
// the queue reflects backend transitions without polling.
export function useActivityQueue(): {
  entries: ActivityQueueEntry[];
  dismiss: (id: string) => Promise<void>;
} {
  const [entries, setEntries] = React.useState<ActivityQueueEntry[]>([]);

  React.useEffect(() => {
    let cancelled = false;
    void ListDeploys().then((initial) => {
      if (cancelled) return;
      setEntries(sortDeployEntries((initial as ActivityQueueEntry[]) ?? []));
    });
    const off = EventsOn(deployStateEvent, (entry: ActivityQueueEntry) => {
      setEntries((prev) => mergeDeployEntry(prev, entry));
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

  return { entries, dismiss };
}

// useTerminalActivityLockState exposes the live map of session lock states keyed by
// terminal sessionId. Frontend renders a lock overlay on any terminal whose
// id is present in the map. The hook also exposes the deployTarget so the
// overlay can show "Waiting for deploy of team/dev 1.0.0".
export function useTerminalActivityLockState(): Map<number, ActivityLockEvent> {
  const [locks, setLocks] = React.useState<Map<number, ActivityLockEvent>>(() => new Map());

  React.useEffect(() => {
    const off = EventsOn(deployLockEvent, (event: ActivityLockEvent) => {
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

function mergeDeployEntry(prev: ActivityQueueEntry[], entry: ActivityQueueEntry): ActivityQueueEntry[] {
  const idx = prev.findIndex((existing) => existing.id === entry.id);
  if (idx === -1) {
    return sortDeployEntries([entry, ...prev]);
  }
  const next = prev.slice();
  next[idx] = entry;
  return sortDeployEntries(next);
}

function sortDeployEntries(entries: ActivityQueueEntry[]): ActivityQueueEntry[] {
  const copy = entries.slice();
  copy.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
  return copy;
}

// activeActivityForSelection finds the running entry that targets the given
// tenant/environment. Used by the deploy button gate.
export function activeActivityForSelection(entries: ActivityQueueEntry[], tenant: string, environment: string): ActivityQueueEntry | null {
  return entries.find((entry) => entry.status === 'running' && entry.tenant === tenant && entry.environment === environment) ?? null;
}

// formatElapsed renders an ISO start timestamp as a humanized "1m12s" string
// using the supplied "now" (so callers can drive consistent ticks via
// state without React.useEffect each second).
export function formatElapsed(startedAt: string, now: number = Date.now()): string {
  const start = Date.parse(startedAt);
  if (Number.isNaN(start)) {
    return '';
  }
  const seconds = Math.max(0, Math.floor((now - start) / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remSeconds = seconds % 60;
  if (minutes < 60) {
    return `${minutes}m${remSeconds}s`;
  }
  const hours = Math.floor(minutes / 60);
  const remMinutes = minutes % 60;
  return `${hours}h${remMinutes}m`;
}

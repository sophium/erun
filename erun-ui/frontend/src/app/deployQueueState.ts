import * as React from 'react';

import { DismissDeploy, ListDeploys } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

export type DeployQueueStatus = 'running' | 'succeeded' | 'failed' | 'skipped';

export type DeployQueueContainerStatus = {
  name: string;
  image: string;
  phase: string;
  ready: boolean;
  restarts: number;
  reason?: string;
  message?: string;
};

export type DeployQueueEntry = {
  id: string;
  tenant: string;
  environment: string;
  version?: string;
  release: string;
  namespace: string;
  kubernetesContext: string;
  status: DeployQueueStatus;
  startedAt: string;
  endedAt?: string;
  lastUpdated: string;
  containers?: DeployQueueContainerStatus[];
  error?: string;
};

export type DeployLockEvent = {
  sessionId: number;
  tenant: string;
  environment: string;
  locked: boolean;
  deployId?: string;
  reason?: string;
  deployTarget?: string;
};

const deployStateEvent = 'deploy:state';
const deployLockEvent = 'deploy:lock';

// useDeployQueue subscribes to the backend deploy:state stream and exposes a
// stable, sorted snapshot to React. Initial state is fetched once via
// ListDeploys; subsequent updates are merged in place from event payloads so
// the queue reflects backend transitions without polling.
export function useDeployQueue(): {
  entries: DeployQueueEntry[];
  dismiss: (id: string) => Promise<void>;
} {
  const [entries, setEntries] = React.useState<DeployQueueEntry[]>([]);

  React.useEffect(() => {
    let cancelled = false;
    void ListDeploys().then((initial) => {
      if (cancelled) return;
      setEntries(sortDeployEntries((initial as DeployQueueEntry[]) ?? []));
    });
    const off = EventsOn(deployStateEvent, (entry: DeployQueueEntry) => {
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

// useTerminalLockState exposes the live map of session lock states keyed by
// terminal sessionId. Frontend renders a lock overlay on any terminal whose
// id is present in the map. The hook also exposes the deployTarget so the
// overlay can show "Waiting for deploy of team/dev 1.0.0".
export function useTerminalLockState(): Map<number, DeployLockEvent> {
  const [locks, setLocks] = React.useState<Map<number, DeployLockEvent>>(() => new Map());

  React.useEffect(() => {
    const off = EventsOn(deployLockEvent, (event: DeployLockEvent) => {
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

function mergeDeployEntry(prev: DeployQueueEntry[], entry: DeployQueueEntry): DeployQueueEntry[] {
  const idx = prev.findIndex((existing) => existing.id === entry.id);
  if (idx === -1) {
    return sortDeployEntries([entry, ...prev]);
  }
  const next = prev.slice();
  next[idx] = entry;
  return sortDeployEntries(next);
}

function sortDeployEntries(entries: DeployQueueEntry[]): DeployQueueEntry[] {
  const copy = entries.slice();
  copy.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
  return copy;
}

// activeDeployForSelection finds the running entry that targets the given
// tenant/environment. Used by the deploy button gate.
export function activeDeployForSelection(entries: DeployQueueEntry[], tenant: string, environment: string): DeployQueueEntry | null {
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

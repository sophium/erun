import { createSelector } from '@reduxjs/toolkit';
import * as React from 'react';

import {
  useCancelWaitingActionMutation,
  useDismissDeployMutation,
  useForceDismissActivityMutation,
  useKillSessionMutationMutation,
  useListDeploysQuery,
  useRecoverPendingHelmReleaseMutation,
} from './api/deployApi';
import { useAppDispatch, useAppSelector } from './hooks';
import {
  removeActivityEntriesForSession,
  removeActivityEntry,
  setActivityEntries,
} from './slices/activitySlice';
import type { RootState } from './store';

export type ActivityQueueStatus =
  | 'waiting'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'skipped'
  | 'cancelled';

export type ActivityQueueSource = 'helm' | 'shell' | 'trace' | 'action' | '';

export interface ActivityQueueContainerStatus {
  name: string;
  image: string;
  phase: string;
  ready: boolean;
  restarts: number;
  reason?: string;
  message?: string;
}

export interface ActivityRecoveryResult {
  ok: boolean;
  output: string;
  error?: string;
}

export interface ActivityQueueEntry {
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
  source?: ActivityQueueSource;
  sessionId?: string;
  actionKind?: string;
  enqueuedAt?: string;
  startedRunningAt?: string;
}

export interface ActivityLockEvent {
  sessionId: number;
  tenant: string;
  environment: string;
  locked: boolean;
  deployId?: string;
  reason?: string;
  deployTarget?: string;
}

const selectActivityEntries = (state: RootState) => state.activity.entries;
const selectLocksBySession = (state: RootState) => state.activity.locksBySession;

// Locks live in Redux as a Record keyed by sessionId so reducers stay
// serializable. Consumers expect a Map for the existing call sites, so we
// memoize the Map adaptation.
const selectLocksMap = createSelector([selectLocksBySession], (locks) => {
  const map = new Map<number, ActivityLockEvent>();
  for (const [key, value] of Object.entries(locks)) {
    const numeric = Number(key);
    if (Number.isFinite(numeric)) {
      map.set(numeric, value);
    }
  }
  return map;
});

export function useActivityQueue(): {
  entries: ActivityQueueEntry[];
  dismiss: (id: string) => Promise<void>;
  forceDismiss: (id: string) => Promise<void>;
  recoverPendingHelm: (id: string) => Promise<ActivityRecoveryResult>;
  killSession: (sessionId: number) => Promise<boolean>;
  cancelWaiting: (id: string) => Promise<boolean>;
} {
  const dispatch = useAppDispatch();
  const entries = useAppSelector(selectActivityEntries);
  const { data: initial } = useListDeploysQuery();
  const [dismissDeploy] = useDismissDeployMutation();
  const [forceDismissActivity] = useForceDismissActivityMutation();
  const [recoverPendingHelmRelease] = useRecoverPendingHelmReleaseMutation();
  const [killSessionMutate] = useKillSessionMutationMutation();
  const [cancelWaitingActionMutate] = useCancelWaitingActionMutation();

  React.useEffect(() => {
    if (initial) {
      dispatch(setActivityEntries(initial));
    }
  }, [dispatch, initial]);

  const dismiss = React.useCallback(
    async (id: string): Promise<void> => {
      const ok = await dismissDeploy(id).unwrap();
      if (ok) {
        dispatch(removeActivityEntry(id));
      }
    },
    [dispatch, dismissDeploy],
  );

  const forceDismiss = React.useCallback(
    async (id: string): Promise<void> => {
      const ok = await forceDismissActivity(id).unwrap();
      if (ok) {
        dispatch(removeActivityEntry(id));
      }
    },
    [dispatch, forceDismissActivity],
  );

  const recoverPendingHelm = React.useCallback(
    async (id: string): Promise<ActivityRecoveryResult> => {
      const result = await recoverPendingHelmRelease(id).unwrap();
      if (result.ok) {
        dispatch(removeActivityEntry(id));
      }
      return result;
    },
    [dispatch, recoverPendingHelmRelease],
  );

  const killSession = React.useCallback(
    async (sessionId: number): Promise<boolean> => {
      if (!Number.isFinite(sessionId) || sessionId <= 0) return false;
      const ok = await killSessionMutate(sessionId).unwrap();
      if (ok) {
        dispatch(removeActivityEntriesForSession(sessionId));
      }
      return ok;
    },
    [dispatch, killSessionMutate],
  );

  const cancelWaiting = React.useCallback(
    async (id: string): Promise<boolean> => {
      const ok = await cancelWaitingActionMutate(id).unwrap();
      return ok;
    },
    [cancelWaitingActionMutate],
  );

  return { entries, dismiss, forceDismiss, recoverPendingHelm, killSession, cancelWaiting };
}

export function useTerminalActivityLockState(): Map<number, ActivityLockEvent> {
  return useAppSelector(selectLocksMap);
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
    raw = `${String(seconds)}s`;
  } else {
    const minutes = Math.floor(seconds / 60);
    const remSeconds = seconds % 60;
    if (minutes < 60) {
      raw = `${String(minutes)}m${String(remSeconds)}s`;
    } else {
      const hours = Math.floor(minutes / 60);
      const remMinutes = minutes % 60;
      raw = `${String(hours)}h${String(remMinutes)}m`;
    }
  }
  return raw.padStart(6, ' ');
}

export function activeActivityForSelection(
  entries: ActivityQueueEntry[],
  tenant: string,
  environment: string,
): ActivityQueueEntry | null {
  return (
    entries.find(
      (entry) =>
        entry.status === 'running' && entry.tenant === tenant && entry.environment === environment,
    ) ?? null
  );
}

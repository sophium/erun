import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { DiffResult } from '@/types';

import type { ReachabilityKind } from '../reconnectCopy';
import { RECONNECT_LINE_BUFFER_LIMIT, type ReconnectState } from '../state';

export type ReviewScope = 'current' | 'commit' | 'all';

// EnvDiffState is one environment's diff, error, scope and commit; each env has
// its own so one stopped env cannot blank the others.
export interface EnvDiffState {
  diff: DiffResult | null;
  loading: boolean;
  error: string;
  errorReconnectable: boolean;
  // Only meaningful while errorReconnectable; lets a stopped env render as informational.
  errorKind: ReachabilityKind;
  scope: ReviewScope;
  commit: string;
}

export const emptyEnvDiffState: EnvDiffState = {
  diff: null,
  loading: false,
  error: '',
  errorReconnectable: false,
  errorKind: 'stale-forward',
  scope: 'current',
  commit: '',
};

export interface ReviewState {
  // Keyed "<tenant>/<environment>"; sparse so missing keys type as undefined.
  diffByEnv: Record<string, EnvDiffState | undefined>;
  reconnect: ReconnectState;
  // Keyed "<envKey>:<path>" since paths collide across environments.
  selectedDiffPath: string;
  // A substring match, not an identity, so it stays global across sections.
  diffFilter: string;
  collapsedDiffDirs: string[];
}

const initialState: ReviewState = {
  diffByEnv: {},
  reconnect: {
    status: 'idle',
    tenant: '',
    environment: '',
    kind: 'stale-forward',
    lines: [],
    error: '',
  },
  selectedDiffPath: '',
  diffFilter: '',
  collapsedDiffDirs: [],
};

function envSlot(state: ReviewState, envKey: string): EnvDiffState {
  const existing = state.diffByEnv[envKey];
  if (existing) {
    return existing;
  }
  const created = { ...emptyEnvDiffState };
  state.diffByEnv[envKey] = created;
  return created;
}

export const reviewSlice = createSlice({
  name: 'review',
  initialState,
  reducers: {
    setEnvDiff(state, action: PayloadAction<{ envKey: string; diff: DiffResult | null }>) {
      envSlot(state, action.payload.envKey).diff = action.payload.diff;
    },
    setEnvDiffLoading(state, action: PayloadAction<{ envKey: string; loading: boolean }>) {
      envSlot(state, action.payload.envKey).loading = action.payload.loading;
    },
    setEnvDiffError(
      state,
      action: PayloadAction<{
        envKey: string;
        error: string;
        reconnectable: boolean;
        kind?: ReachabilityKind;
      }>,
    ) {
      const slot = envSlot(state, action.payload.envKey);
      slot.error = action.payload.error;
      slot.errorReconnectable = action.payload.reconnectable;
      slot.errorKind = action.payload.kind ?? 'stale-forward';
    },
    pruneEnvDiffs(state, action: PayloadAction<string[]>) {
      const keep = new Set(action.payload);
      state.diffByEnv = Object.fromEntries(
        Object.entries(state.diffByEnv).filter(([key]) => keep.has(key)),
      );
    },
    setReconnect(state, action: PayloadAction<ReconnectState>) {
      state.reconnect = action.payload;
    },
    appendReconnectLine(state, action: PayloadAction<string>) {
      state.reconnect.lines.push(action.payload);
      if (state.reconnect.lines.length > RECONNECT_LINE_BUFFER_LIMIT) {
        state.reconnect.lines.splice(0, state.reconnect.lines.length - RECONNECT_LINE_BUFFER_LIMIT);
      }
    },
    setSelectedDiffPath(state, action: PayloadAction<string>) {
      state.selectedDiffPath = action.payload;
    },
    setEnvReviewScope(state, action: PayloadAction<{ envKey: string; scope: ReviewScope }>) {
      envSlot(state, action.payload.envKey).scope = action.payload.scope;
    },
    setEnvReviewCommit(state, action: PayloadAction<{ envKey: string; commit: string }>) {
      envSlot(state, action.payload.envKey).commit = action.payload.commit;
    },
    setDiffFilter(state, action: PayloadAction<string>) {
      state.diffFilter = action.payload;
    },
    toggleDiffDirCollapsed(state, action: PayloadAction<string>) {
      const idx = state.collapsedDiffDirs.indexOf(action.payload);
      if (idx >= 0) {
        state.collapsedDiffDirs.splice(idx, 1);
      } else {
        state.collapsedDiffDirs.push(action.payload);
      }
    },
    clearDiffDirsCollapsed(state) {
      state.collapsedDiffDirs = [];
    },
    setAll(_state, action: PayloadAction<ReviewState>) {
      return action.payload;
    },
  },
});

export const {
  setEnvDiff,
  setEnvDiffLoading,
  setEnvDiffError,
  pruneEnvDiffs,
  setReconnect,
  appendReconnectLine,
  setSelectedDiffPath,
  setEnvReviewScope,
  setEnvReviewCommit,
  setDiffFilter,
  toggleDiffDirCollapsed,
  clearDiffDirsCollapsed,
  setAll: setReviewAll,
} = reviewSlice.actions;

export function diffPathKey(envKey: string, path: string): string {
  return envKey + ':' + path;
}

// diffPathWithinEnv inverts diffPathKey for one env, or '' for another env.
// Matches by prefix because a directory key and its path may both contain ':'.
export function diffPathWithinEnv(envKey: string, keyedPath: string): string {
  const prefix = `${envKey}:`;
  return keyedPath.startsWith(prefix) ? keyedPath.slice(prefix.length) : '';
}

export default reviewSlice.reducer;

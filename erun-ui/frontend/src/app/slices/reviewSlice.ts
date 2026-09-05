import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { DiffResult } from '@/types';

import type { ReachabilityKind } from '../reconnectCopy';
import { RECONNECT_LINE_BUFFER_LIMIT, type ReconnectState } from '../state';

export type ReviewScope = 'current' | 'commit' | 'all';

// EnvDiffState is one environment's diff and everything scoped to it. An
// orchestrator session is cross-env, so the panel holds one of these per linked
// environment rather than one globally (#1178).
//
// Each carries its OWN error, which is the load-bearing part. The single-slot
// shape cleared the one diff on any failure, so one stopped environment blanked
// the diffs of every other linked env -- and an orchestrator's environments are
// rarely all running at once, so that was the everyday state, not an edge case.
//
// scope and commit are per-env too: ReviewBase, ReviewCommits, Scope and
// SelectedCommit are per-repository, so a commit list means nothing shared
// across two unrelated checkouts.
export interface EnvDiffState {
  diff: DiffResult | null;
  loading: boolean;
  error: string;
  errorReconnectable: boolean;
  // Only meaningful while errorReconnectable is true: which of the two
  // reachability shapes this is, so the panel can render a stopped
  // environment as informational rather than a fault (#1230). Defaults to
  // 'stale-forward', the always-a-fault behavior every caller had before the
  // kind existed.
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
  // Keyed "<tenant>/<environment>". A single-environment tab is simply the
  // one-entry case, so there is one code path rather than two.
  //
  // Explicitly sparse: a plain Record<string, EnvDiffState> would type a missing
  // key as present, so every necessary undefined check would read as redundant
  // while still being required at runtime.
  diffByEnv: Record<string, EnvDiffState | undefined>;
  reconnect: ReconnectState;
  // Keyed "<envKey>:<path>". Paths collide across environments -- two linked
  // envs can both have AGENTS.md -- and this and collapsedDiffDirs are both
  // path-keyed, so an unkeyed value silently aliases one env's file onto
  // another's.
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

// envSlot returns the mutable slot for an environment, creating it on first
// write so a caller never has to check.
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
    // Drop the environments no longer in scope, so switching from a two-env
    // orchestrator to a single env tab does not leave stale sections rendering.
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

// diffPathKey namespaces a file path by its environment. Two linked
// environments can both have AGENTS.md, and selectedDiffPath / collapsedDiffDirs
// are both path-keyed, so an unkeyed path silently aliases one env's file onto
// another's (#1178).
export function diffPathKey(envKey: string, path: string): string {
  return envKey + ':' + path;
}
export default reviewSlice.reducer;

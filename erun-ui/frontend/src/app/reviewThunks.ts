import type { DiffResult } from '@/types';

import { reviewApi } from './api/reviewApi';
import { sessionApi } from './api/sessionApi';
import { pruneStaleDiffReviewStatuses } from './diffReviewStatusThunks';
import { chooseSelectedDiffPath } from './diffUtils';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import {
  mcpUnreachableKind,
  type ReachabilityKind,
  stripMcpUnreachableMarker,
} from './reconnectCopy';
import { scrollSelectedDiffIntoView } from './reviewDiffNavigation';
import { type ReviewTarget, selectReviewTargets } from './selectors';
import { setChangedFilesOpen } from './slices/layoutSlice';
import { bumpReviewDiff } from './slices/requestCountersSlice';
import type { ReviewScope } from './slices/reviewSlice';
import {
  diffPathKey,
  diffPathWithinEnv,
  emptyEnvDiffState,
  pruneEnvDiffs,
  setDiffFilter as setDiffFilterAction,
  setEnvDiff,
  setEnvDiffError,
  setEnvDiffLoading,
  setEnvReviewCommit,
  setEnvReviewScope,
  setReconnect,
  setSelectedDiffPath,
  toggleDiffDirCollapsed,
} from './slices/reviewSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export const setDiffFilter =
  (value: string): AppThunk =>
  (dispatch) => {
    dispatch(setDiffFilterAction(value.trim().toLowerCase()));
  };

export const toggleChangedFiles = (): AppThunk => (dispatch, getState) => {
  dispatch(setChangedFilesOpen(!getState().layout.changedFilesOpen));
};

export const toggleDiffDirectory =
  (path: string): AppThunk =>
  (dispatch) => {
    dispatch(toggleDiffDirCollapsed(path));
  };

// selectDiffPath takes the file the tree's node names -- its environment and
// its bare path, the two things the DOM actually carries. It keys the first
// with the second for the store, the one representation every reader of
// selectedDiffPath agrees on, and hands the bare path to the scroll, which
// looks the section up by `data-path`.
export const selectDiffPath =
  (envKey: string, path: string): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    dispatch(setSelectedDiffPath(diffPathKey(envKey, path)));
    window.setTimeout(() => {
      scrollSelectedDiffIntoView(controller.diffList, path);
    }, 0);
  };

// selectReviewRange is per-environment: each linked env has its own commit
// list, so a scope or commit chosen in one section means nothing in another.
export const selectReviewRange =
  (envKey: string, scope: ReviewScope, hash = ''): AppThunk =>
  (dispatch, getState) => {
    const slot = getState().review.diffByEnv[envKey] ?? emptyEnvDiffState;
    const selected = hash.trim();
    if ((scope === slot.scope && selected === slot.commit) || slot.loading) {
      return;
    }
    dispatch(setEnvReviewScope({ envKey, scope }));
    dispatch(setEnvReviewCommit({ envKey, commit: selected }));
    void dispatch(loadReviewDiff());
  };

function applyReviewDiffSuccess(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  envKey: string,
  diff: DiffResult,
): void {
  dispatch(setEnvDiff({ envKey, diff }));
  dispatch(setEnvDiffError({ envKey, error: '', reconnectable: false }));
  dispatch(setEnvReviewScope({ envKey, scope: diff.scope ?? 'current' }));
  dispatch(setEnvReviewCommit({ envKey, commit: diff.selectedCommit ?? '' }));
  // selectedDiffPath is stored env-keyed, so both halves of the round trip
  // through chooseSelectedDiffPath (which speaks the bare path the diff's own
  // `files` carry) happen here: strip this env's prefix in, re-key out.
  const current = diffPathWithinEnv(envKey, getState().review.selectedDiffPath);
  const chosen = chooseSelectedDiffPath(diff, current);
  if (chosen) {
    dispatch(setSelectedDiffPath(diffPathKey(envKey, chosen)));
  }
}

// applyReviewDiffFailure writes only THIS environment's slot. The single-slot
// version cleared the one shared diff, so one stopped environment blanked every
// other linked env's diff -- and an orchestrator's environments are rarely all
// running at once, so that was the everyday case (#1178).
function applyReviewDiffFailure(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  envKey: string,
  error: unknown,
  silent: boolean,
): void {
  const currentDiff = getState().review.diffByEnv[envKey]?.diff ?? null;
  if (silent && currentDiff) {
    return;
  }
  if (!silent || !currentDiff) {
    dispatch(setEnvDiff({ envKey, diff: null }));
  }
  const message = readError(error);
  const kind = mcpUnreachableKind(message);
  if (kind) {
    dispatch(
      setEnvDiffError({
        envKey,
        error: stripMcpUnreachableMarker(message),
        reconnectable: true,
        kind,
      }),
    );
  } else {
    dispatch(setEnvDiffError({ envKey, error: message, reconnectable: false }));
  }
}

// reviewTargets is selectReviewTargets, named here so the fetch sites below read
// as "the targets this panel is showing" rather than reaching into selectors at
// every one of them.
function reviewTargets(state: ReturnType<typeof import('./store').store.getState>): ReviewTarget[] {
  return selectReviewTargets(state);
}

// fetchTargetDiff asks the desktop for one target's diff through the endpoint
// that matches what the target is: an environment's arrives over its MCP edge
// (LoadDiff resolves the env and its port), while a directory's is read with host
// git from the path itself. The running-query wait before each initiate is the
// pre-existing guard against a manual refresh silently inheriting an in-flight
// periodic tick's result -- see loadOneReviewDiff's own note below.
async function fetchTargetDiff(
  dispatch: Parameters<AppThunk>[0],
  target: ReviewTarget,
  options: { scope: string; selectedCommit: string; target: string },
): Promise<DiffResult> {
  if (target.kind === 'directory') {
    const args = { directory: target.directory, options };
    await dispatch(reviewApi.util.getRunningQueryThunk('getDirectoryDiff', args));
    return dispatch(
      reviewApi.endpoints.getDirectoryDiff.initiate(args, { forceRefetch: true }),
    ).unwrap();
  }
  const args = {
    selection: { tenant: target.tenant, environment: target.environment },
    options,
  };
  await dispatch(reviewApi.util.getRunningQueryThunk('getDiff', args));
  return dispatch(reviewApi.endpoints.getDiff.initiate(args, { forceRefetch: true })).unwrap();
}

// loadOneReviewDiff fetches and applies one target's diff. Every failure is
// contained here, so allSettled below cannot let one target's error stop
// another's fetch from being applied.
async function loadOneReviewDiff(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  target: ReviewTarget,
  options: { silent?: boolean },
): Promise<void> {
  const { envKey } = target;
  const slot = getState().review.diffByEnv[envKey] ?? emptyEnvDiffState;
  if (!options.silent) {
    dispatch(setEnvDiffLoading({ envKey, loading: true }));
    dispatch(setEnvDiffError({ envKey, error: '', reconnectable: slot.errorReconnectable }));
  }
  // The contribute source is an environment concept -- which clone inside the
  // environment to diff -- so a directory, which has no environment and so no
  // clone, always diffs its own working tree.
  const diffOptions = {
    scope: slot.scope,
    selectedCommit: slot.commit,
    target:
      target.kind === 'env' ? (getState().contribute.diffSourceByEnv[envKey] ?? 'env') : 'env',
  };
  try {
    // The periodic silent refresh (scheduleReviewDiffRefresh) and a manual
    // "Refresh diff" click both land here for the same env with identical
    // args, and the timer's own re-arm only guards against stacking a second
    // tick on itself -- not against a manual click arriving mid-tick. RTK
    // Query's condition() bails a forced refetch out from under a pending
    // request for the same query before ever looking at forceRefetch (see
    // erun#1953's getInitialState fix), so without this wait the click would
    // silently inherit the in-flight tick's result instead of its own.
    const diff = await fetchTargetDiff(dispatch, target, diffOptions);
    applyReviewDiffSuccess(dispatch, getState, envKey, diff);
  } catch (error: unknown) {
    applyReviewDiffFailure(dispatch, getState, envKey, error, Boolean(options.silent));
  } finally {
    if (!options.silent) {
      dispatch(setEnvDiffLoading({ envKey, loading: false }));
    }
  }
}

export const loadReviewDiff =
  (options: { silent?: boolean } = {}): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const targets = reviewTargets(getState());
    if (targets.length === 0) {
      return;
    }
    // Drop sections for environments no longer in scope, so switching from a
    // two-env orchestrator to a single env tab does not leave stale ones.
    dispatch(pruneEnvDiffs(targets.map((target) => target.envKey)));
    dispatch(pruneStaleDiffReviewStatuses());
    dispatch(bumpReviewDiff());
    const request = getState().requestCounters.reviewDiff;
    const scopeKey = targets.map((target) => target.envKey).join(',');

    // One fetch per environment, each over that env's own MCP. allSettled, not
    // all: a stopped environment must not cancel the others, and every failure
    // is already contained inside loadOneReviewDiff.
    await Promise.allSettled(
      targets.map((target) => loadOneReviewDiff(dispatch, getState, target, options)),
    );

    if (!isCurrentReviewDiffRequest(getState, request, scopeKey)) {
      return;
    }
    scheduleReviewDiffRefresh(dispatch, getState, controller);
  };

export const refreshReviewDiff = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  if (reviewTargets(getState()).length === 0) {
    return;
  }
  await dispatch(loadReviewDiff());
  const anyError = Object.values(getState().review.diffByEnv).some(
    (slot) => (slot?.error ?? '') !== '',
  );
  if (!anyError) {
    dispatch(showNotification('success', 'Diff refreshed.'));
  }
};

function isCurrentReviewDiffRequest(
  getState: () => ReturnType<typeof import('./store').store.getState>,
  request: number,
  scopeKey: string,
): boolean {
  const state = getState();
  const currentScopeKey = reviewTargets(state)
    .map((target) => target.envKey)
    .join(',');
  return request === state.requestCounters.reviewDiff && scopeKey === currentScopeKey;
}

function scheduleReviewDiffRefresh(
  dispatch: (thunk: AppThunk<Promise<void>>) => Promise<void>,
  getState: () => ReturnType<typeof import('./store').store.getState>,
  controller: NonNullable<ReturnType<typeof requireController>>,
  delay = REVIEW_DIFF_REFRESH_INTERVAL_MS,
): void {
  controller.cancelReviewDiffRefresh();
  const state = getState();
  // An orchestrator session has linked environments but no sidebar selection
  // of its own, so this arms off the resolved env set rather than
  // state.selection.selected -- which would have stopped the refresh outright
  // for exactly the cross-env case (#1178). Keep this guard identical to the
  // timer callback's own below; a mismatch here left periodic review-diff
  // refresh permanently dead for every orchestrator session.
  if (!state.layout.reviewOpen || reviewTargets(state).length === 0) {
    return;
  }
  controller.scheduleReviewDiffRefreshTimer(() => {
    const next = getState();
    if (!next.layout.reviewOpen || reviewTargets(next).length === 0) {
      controller.stopReviewDiffRefresh();
      return;
    }
    // Skip a tick while any section is still fetching, so a slow environment
    // does not get a second in-flight request stacked on the first.
    if (Object.values(next.review.diffByEnv).some((slot) => slot?.loading)) {
      scheduleReviewDiffRefresh(dispatch, getState, controller);
      return;
    }
    void dispatch(loadReviewDiff({ silent: true }));
  }, delay);
}

const idleReconnect = () => ({
  status: 'idle' as const,
  tenant: '',
  environment: '',
  kind: 'stale-forward' as const,
  lines: [] as string[],
  error: '',
});

// requestReconnect takes its target explicitly from the caller (the specific
// linked environment whose card was clicked) rather than the sidebar's
// globally-selected environment. An orchestrator session shows one card per
// linked environment, and the selected env is rarely the one that failed, so
// deriving the target from selection.selected reconnected the wrong
// environment.
export const requestReconnect =
  (tenant: string, environment: string, kind: ReachabilityKind): AppThunk =>
  (dispatch) => {
    dispatch(
      setReconnect({
        status: 'confirm',
        tenant,
        environment,
        kind,
        lines: [],
        error: '',
      }),
    );
  };

export const cancelReconnect = (): AppThunk => (dispatch, getState) => {
  if (getState().review.reconnect.status === 'running') {
    return;
  }
  dispatch(setReconnect(idleReconnect()));
};

export const confirmReconnect = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const { tenant, environment, kind, status } = getState().review.reconnect;
  if (!tenant || !environment || status === 'running') {
    return;
  }
  dispatch(
    setReconnect({
      status: 'running',
      tenant,
      environment,
      kind,
      lines: [],
      error: '',
    }),
  );
  try {
    await dispatch(sessionApi.endpoints.reconnectMCP.initiate({ tenant, environment })).unwrap();
    dispatch(setReconnect(idleReconnect()));
    await dispatch(loadReviewDiff());
  } catch (error: unknown) {
    // Preserve the env scope and the accumulated lines so the user can see
    // what the redeploy was doing when it failed.
    const reconnect = getState().review.reconnect;
    dispatch(
      setReconnect({
        status: 'error',
        tenant: reconnect.tenant,
        environment: reconnect.environment,
        kind: reconnect.kind,
        lines: reconnect.lines,
        error: readError(error),
      }),
    );
  }
};

// dismissReconnect closes the error surface after the user has read it.
export const dismissReconnect = (): AppThunk => (dispatch, getState) => {
  if (getState().review.reconnect.status !== 'error') {
    return;
  }
  dispatch(setReconnect(idleReconnect()));
};

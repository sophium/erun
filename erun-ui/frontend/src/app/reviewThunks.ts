import type { DiffResult, UISelection } from '@/types';

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
import { selectReviewEnvTargets } from './selectors';
import { setChangedFilesOpen } from './slices/layoutSlice';
import { bumpReviewDiff } from './slices/requestCountersSlice';
import type { ReviewScope } from './slices/reviewSlice';
import {
  diffPathKey,
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

export const selectDiffPath =
  (path: string): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    dispatch(setSelectedDiffPath(path));
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
  const chosen = chooseSelectedDiffPath(diff, getState().review.selectedDiffPath);
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

// reviewEnvTargets is selectReviewEnvTargets shaped for the fetch: the same
// resolution, plus the UISelection each LoadDiff call needs.
function reviewEnvTargets(
  state: ReturnType<typeof import('./store').store.getState>,
): { envKey: string; selection: UISelection }[] {
  return selectReviewEnvTargets(state).map((target) => ({
    envKey: target.envKey,
    selection: { tenant: target.tenant, environment: target.environment },
  }));
}

// loadOneReviewDiff fetches and applies one environment's diff. Every failure is
// contained here, so allSettled below cannot let one environment's error stop
// another's fetch from being applied.
async function loadOneReviewDiff(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  target: { envKey: string; selection: UISelection },
  options: { silent?: boolean },
): Promise<void> {
  const { envKey, selection } = target;
  const slot = getState().review.diffByEnv[envKey] ?? emptyEnvDiffState;
  const contributeTarget = getState().contribute.diffSourceByEnv[envKey] ?? 'env';
  if (!options.silent) {
    dispatch(setEnvDiffLoading({ envKey, loading: true }));
    dispatch(setEnvDiffError({ envKey, error: '', reconnectable: slot.errorReconnectable }));
  }
  const diffArgs = {
    selection,
    options: { scope: slot.scope, selectedCommit: slot.commit, target: contributeTarget },
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
    await dispatch(reviewApi.util.getRunningQueryThunk('getDiff', diffArgs));
    const diff = await dispatch(
      reviewApi.endpoints.getDiff.initiate(diffArgs, { forceRefetch: true }),
    ).unwrap();
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
    const targets = reviewEnvTargets(getState());
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
  if (reviewEnvTargets(getState()).length === 0) {
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
  const currentScopeKey = reviewEnvTargets(state)
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
  if (!state.layout.reviewOpen || !state.selection.selected) {
    return;
  }
  controller.scheduleReviewDiffRefreshTimer(() => {
    const next = getState();
    // An orchestrator session has linked environments but no sidebar selection
    // of its own, so the timer arms off the resolved env set rather than
    // state.selection.selected -- which would have stopped the refresh outright
    // for exactly the cross-env case (#1178).
    if (!next.layout.reviewOpen || reviewEnvTargets(next).length === 0) {
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
// environment (#1230).
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

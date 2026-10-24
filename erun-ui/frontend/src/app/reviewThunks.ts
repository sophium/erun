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

// selectDiffPath stores the env-keyed path and scrolls by the bare `data-path`.
export const selectDiffPath =
  (envKey: string, path: string): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    dispatch(setSelectedDiffPath(diffPathKey(envKey, path)));
    window.setTimeout(() => {
      scrollSelectedDiffIntoView(controller.diffList, path);
    }, 0);
  };

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
  // selectedDiffPath is env-keyed; chooseSelectedDiffPath speaks bare paths.
  const current = diffPathWithinEnv(envKey, getState().review.selectedDiffPath);
  const chosen = chooseSelectedDiffPath(diff, current);
  if (chosen) {
    dispatch(setSelectedDiffPath(diffPathKey(envKey, chosen)));
  }
}

// applyReviewDiffFailure writes only this env's slot so other linked envs keep their diffs.
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

function reviewTargets(state: ReturnType<typeof import('./store').store.getState>): ReviewTarget[] {
  return selectReviewTargets(state);
}

// fetchTargetDiff reads an environment's diff over its MCP edge and a
// directory's with host git.
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
  // A directory has no environment clone, so it always diffs its own tree.
  const diffOptions = {
    scope: slot.scope,
    selectedCommit: slot.commit,
    target:
      target.kind === 'env' ? (getState().contribute.diffSourceByEnv[envKey] ?? 'env') : 'env',
  };
  try {
    // RTK Query's condition() drops a forced refetch while the same query is
    // pending, so a manual refresh mid-tick must wait or it inherits the stale result.
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
    dispatch(pruneEnvDiffs(targets.map((target) => target.envKey)));
    dispatch(pruneStaleDiffReviewStatuses());
    dispatch(bumpReviewDiff());
    const request = getState().requestCounters.reviewDiff;
    const scopeKey = targets.map((target) => target.envKey).join(',');

    // allSettled: one stopped environment must not cancel the others.
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
  // Orchestrator sessions have no sidebar selection; keep this guard identical
  // to the timer callback's or periodic refresh dies for them.
  if (!state.layout.reviewOpen || reviewTargets(state).length === 0) {
    return;
  }
  controller.scheduleReviewDiffRefreshTimer(() => {
    const next = getState();
    if (!next.layout.reviewOpen || reviewTargets(next).length === 0) {
      controller.stopReviewDiffRefresh();
      return;
    }
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

// requestReconnect targets the clicked env, not selection.selected, which is
// rarely the one that failed in an orchestrator session.
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

export const dismissReconnect = (): AppThunk => (dispatch, getState) => {
  if (getState().review.reconnect.status !== 'error') {
    return;
  }
  dispatch(setReconnect(idleReconnect()));
};

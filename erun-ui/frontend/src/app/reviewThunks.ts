import type { DiffResult } from '@/types';

import { reviewApi } from './api/reviewApi';
import { sessionApi } from './api/sessionApi';
import { chooseSelectedDiffPath } from './diffUtils';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { isMcpUnreachableMessage, stripMcpUnreachableMarker } from './reconnectCopy';
import { scrollSelectedDiffIntoView } from './reviewDiffNavigation';
import { setChangedFilesOpen } from './slices/layoutSlice';
import { bumpReviewDiff } from './slices/requestCountersSlice';
import type { ReviewState } from './slices/reviewSlice';
import {
  setDiff,
  setDiffError,
  setDiffFilter as setDiffFilterAction,
  setDiffLoading,
  setReconnect,
  setSelectedDiffPath,
  setSelectedReviewCommit,
  setSelectedReviewScope,
  toggleDiffDirCollapsed,
} from './slices/reviewSlice';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// reviewThunks own the diff/review-panel state: filter input, scope selector,
// directory expansion, refresh polling, and the MCP-reconnect dialog flow.

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

export const selectReviewRange =
  (scope: ReviewState['selectedReviewScope'], hash = ''): AppThunk =>
  (dispatch, getState) => {
    const review = getState().review;
    const selected = hash.trim();
    if (
      (scope === review.selectedReviewScope && selected === review.selectedReviewCommit) ||
      review.diffLoading
    ) {
      return;
    }
    dispatch(setSelectedReviewScope(scope));
    dispatch(setSelectedReviewCommit(selected));
    void dispatch(loadReviewDiff());
  };

function applyReviewDiffSuccess(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  diff: DiffResult,
): void {
  dispatch(setDiff(diff));
  dispatch(setDiffError({ error: '', reconnectable: false }));
  dispatch(setSelectedReviewScope(diff.scope ?? 'current'));
  dispatch(setSelectedReviewCommit(diff.selectedCommit ?? ''));
  dispatch(setSelectedDiffPath(chooseSelectedDiffPath(diff, getState().review.selectedDiffPath)));
}

function applyReviewDiffFailure(
  dispatch: Parameters<AppThunk>[0],
  getState: () => ReturnType<typeof import('./store').store.getState>,
  error: unknown,
  silent: boolean,
): void {
  const currentDiff = getState().review.diff;
  if (silent && currentDiff) {
    return;
  }
  if (!silent || !currentDiff) {
    dispatch(setDiff(null));
  }
  const message = readError(error);
  if (isMcpUnreachableMessage(message)) {
    dispatch(setDiffError({ error: stripMcpUnreachableMarker(message), reconnectable: true }));
  } else {
    dispatch(setDiffError({ error: message, reconnectable: false }));
  }
}

export const loadReviewDiff =
  (options: { silent?: boolean } = {}): AppThunk<Promise<void>> =>
  async (dispatch, getState, extra) => {
    const controller = requireController(extra);
    const state = getState();
    const selection = state.selection.selected;
    if (!selection) {
      return;
    }
    dispatch(bumpReviewDiff());
    const request = getState().requestCounters.reviewDiff;
    const selectedKey = selectionKey(selection);
    const scope = state.review.selectedReviewScope;
    const selectedCommit = state.review.selectedReviewCommit;
    if (!options.silent) {
      dispatch(setDiffLoading(true));
      dispatch(
        setDiffError({ error: '', reconnectable: getState().review.diffErrorReconnectable }),
      );
    }
    try {
      const diff = await dispatch(
        reviewApi.endpoints.getDiff.initiate(
          { selection, options: { scope, selectedCommit } },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!isCurrentReviewDiffRequest(getState, request, selectedKey)) {
        return;
      }
      applyReviewDiffSuccess(dispatch, getState, diff);
    } catch (error: unknown) {
      if (!isCurrentReviewDiffRequest(getState, request, selectedKey)) {
        return;
      }
      applyReviewDiffFailure(dispatch, getState, error, Boolean(options.silent));
    } finally {
      if (request === getState().requestCounters.reviewDiff) {
        if (!options.silent) {
          dispatch(setDiffLoading(false));
        }
        scheduleReviewDiffRefresh(dispatch, getState, controller);
      }
    }
  };

export const refreshReviewDiff = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  if (!getState().selection.selected) {
    return;
  }
  await dispatch(loadReviewDiff());
  if (!getState().review.diffError) {
    dispatch(showNotification('success', 'Diff refreshed.'));
  }
};

function isCurrentReviewDiffRequest(
  getState: () => ReturnType<typeof import('./store').store.getState>,
  request: number,
  selectedKey: string,
): boolean {
  const state = getState();
  return (
    request === state.requestCounters.reviewDiff &&
    selectedKey === selectionKey(state.selection.selected ?? { tenant: '', environment: '' })
  );
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
    if (!next.layout.reviewOpen || !next.selection.selected) {
      controller.stopReviewDiffRefresh();
      return;
    }
    if (next.review.diffLoading) {
      scheduleReviewDiffRefresh(dispatch, getState, controller);
      return;
    }
    void dispatch(loadReviewDiff({ silent: true }));
  }, delay);
}

// Reconnect dialog ============================================================

export const requestReconnect = (): AppThunk => (dispatch, getState) => {
  if (!getState().selection.selected) {
    return;
  }
  dispatch(setReconnect({ status: 'confirm', lastLine: '', error: '' }));
};

export const cancelReconnect = (): AppThunk => (dispatch, getState) => {
  if (getState().review.reconnect.status === 'running') {
    return;
  }
  dispatch(setReconnect({ status: 'idle', lastLine: '', error: '' }));
};

export const confirmReconnect = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const selection = state.selection.selected;
  if (!selection || state.review.reconnect.status === 'running') {
    return;
  }
  dispatch(setReconnect({ status: 'running', lastLine: '', error: '' }));
  try {
    await dispatch(sessionApi.endpoints.reconnectMCP.initiate(selection)).unwrap();
    dispatch(setReconnect({ status: 'idle', lastLine: '', error: '' }));
    await dispatch(loadReviewDiff());
  } catch (error: unknown) {
    const lastLine = getState().review.reconnect.lastLine;
    dispatch(
      setReconnect({
        status: 'error',
        lastLine,
        error: readError(error),
      }),
    );
  }
};

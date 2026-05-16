import { reviewApi } from './api/reviewApi';
import { sessionApi } from './api/sessionApi';
import { chooseSelectedDiffPath } from './diffUtils';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { isMcpUnreachableMessage, stripMcpUnreachableMarker } from './reconnectCopy';
import { scrollSelectedDiffIntoView } from './reviewDiffNavigation';
import { toggleDiffDirCollapsed } from './slices/reviewSlice';
import type { AppState } from './state';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';
import { selectionKey } from './versionSuggestions';

// reviewThunks own the diff/review-panel state: filter input, scope selector,
// directory expansion, refresh polling, and the MCP-reconnect dialog flow.

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

let reviewDiffRequest = 0;

export const setDiffFilter = (value: string): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    controller.state.diffFilter = value.trim().toLowerCase();
  };

export const toggleChangedFiles = (): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    controller.state.changedFilesOpen = !controller.state.changedFilesOpen;
  };

export const toggleDiffDirectory = (path: string): AppThunk =>
  (dispatch) => {
    dispatch(toggleDiffDirCollapsed(path));
  };

export const selectDiffPath = (path: string): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    controller.state.selectedDiffPath = path;
    window.setTimeout(() => {
      scrollSelectedDiffIntoView(controller.diffList, controller.state.selectedDiffPath);
    }, 0);
  };

export const selectReviewRange = (scope: AppState['selectedReviewScope'], hash = ''): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selected = hash.trim();
    if (
      (scope === controller.state.selectedReviewScope && selected === controller.state.selectedReviewCommit) ||
      controller.state.diffLoading
    ) {
      return;
    }
    controller.state.selectedReviewScope = scope;
    controller.state.selectedReviewCommit = selected;
    void dispatch(loadReviewDiff());
  };

export const loadReviewDiff = (options: { silent?: boolean } = {}): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = controller.state.selected;
    if (!selection) {
      return;
    }
    const request = ++reviewDiffRequest;
    const selectedKey = selectionKey(selection);
    const scope = controller.state.selectedReviewScope;
    const selectedCommit = controller.state.selectedReviewCommit;
    if (!options.silent) {
      controller.state.diffLoading = true;
      controller.state.diffError = '';
    }
    try {
      const diff = await dispatch(
        reviewApi.endpoints.getDiff.initiate(
          { selection, options: { scope, selectedCommit } },
          { forceRefetch: true },
        ),
      ).unwrap();
      if (!isCurrentReviewDiffRequest(controller, request, selectedKey)) {
        return;
      }
      controller.state.diff = diff;
      controller.state.diffError = '';
      controller.state.diffErrorReconnectable = false;
      controller.state.selectedReviewScope = diff.scope || 'current';
      controller.state.selectedReviewCommit = diff.selectedCommit || '';
      controller.state.selectedDiffPath = chooseSelectedDiffPath(diff, controller.state.selectedDiffPath);
    } catch (error: unknown) {
      if (!isCurrentReviewDiffRequest(controller, request, selectedKey)) {
        return;
      }
      if (options.silent && controller.state.diff) {
        return;
      }
      if (!options.silent || !controller.state.diff) {
        controller.state.diff = null;
      }
      const message = readError(error);
      if (isMcpUnreachableMessage(message)) {
        controller.state.diffError = stripMcpUnreachableMarker(message);
        controller.state.diffErrorReconnectable = true;
      } else {
        controller.state.diffError = message;
        controller.state.diffErrorReconnectable = false;
      }
    } finally {
      if (request === reviewDiffRequest) {
        if (!options.silent) {
          controller.state.diffLoading = false;
        }
        scheduleReviewDiffRefresh(dispatch, controller);
      }
    }
  };

export const refreshReviewDiff = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (!controller.state.selected) {
      return;
    }
    await dispatch(loadReviewDiff());
    if (!controller.state.diffError) {
      dispatch(showNotification('success', 'Diff refreshed.'));
    }
  };

function isCurrentReviewDiffRequest(
  controller: NonNullable<ReturnType<typeof requireController>>,
  request: number,
  selectedKey: string,
): boolean {
  return request === reviewDiffRequest &&
    selectedKey === selectionKey(controller.state.selected || { tenant: '', environment: '' });
}

function scheduleReviewDiffRefresh(
  dispatch: (thunk: AppThunk<Promise<void>>) => Promise<void>,
  controller: NonNullable<ReturnType<typeof requireController>>,
  delay = REVIEW_DIFF_REFRESH_INTERVAL_MS,
): void {
  controller.cancelReviewDiffRefresh();
  if (!controller.state.reviewOpen || !controller.state.selected) {
    return;
  }
  controller.scheduleReviewDiffRefreshTimer(() => {
    if (!controller.state.reviewOpen || !controller.state.selected) {
      controller.stopReviewDiffRefresh();
      return;
    }
    if (controller.state.diffLoading) {
      scheduleReviewDiffRefresh(dispatch, controller);
      return;
    }
    void dispatch(loadReviewDiff({ silent: true }));
  }, delay);
}

// Reconnect dialog ============================================================

export const requestReconnect = (): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (!controller.state.selected) {
      return;
    }
    controller.state.reconnect = { status: 'confirm', lastLine: '', error: '' };
  };

export const cancelReconnect = (): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    if (controller.state.reconnect.status === 'running') {
      return;
    }
    controller.state.reconnect = { status: 'idle', lastLine: '', error: '' };
  };

export const confirmReconnect = (): AppThunk<Promise<void>> =>
  async (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    const selection = controller.state.selected;
    if (!selection || controller.state.reconnect.status === 'running') {
      return;
    }
    controller.state.reconnect = { status: 'running', lastLine: '', error: '' };
    try {
      await dispatch(sessionApi.endpoints.reconnectMCP.initiate(selection)).unwrap();
      controller.state.reconnect = { status: 'idle', lastLine: '', error: '' };
      await dispatch(loadReviewDiff());
    } catch (error: unknown) {
      controller.state.reconnect = {
        status: 'error',
        lastLine: controller.state.reconnect.lastLine,
        error: readError(error),
      };
    }
  };

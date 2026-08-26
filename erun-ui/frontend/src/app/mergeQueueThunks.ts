import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchMergeQueueAction } from './slices/mergeQueueActionSlice';
import type { AppThunk } from './store';

// mergeQueueThunks drives the Merge Queue panel's "Advance queue" action: an
// inline confirm step (cancel-before-commitment), then the write itself. The
// target branch is derived from the queue's own rows rather than typed by the
// operator — see mergeQueueTargetBranch in TenantDashboardPanels.tsx.

export const confirmAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: true, error: '' }));
};

export const cancelAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: false, error: '' }));
};

// clearAdvanceMergeQueueError drops a stale advance-queue write error once a
// sign-in it prompted has succeeded, leaving the confirm step in place so the
// operator retries Confirm themselves (#1392).
export const clearAdvanceMergeQueueError = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ error: '' }));
};

export const submitAdvanceMergeQueue =
  (targetBranch: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const { tenant } = getState().tenantDashboard;
    if (!tenant.trim() || !targetBranch) {
      dispatch(patchMergeQueueAction({ error: 'No tenant is open.' }));
      return;
    }
    dispatch(patchMergeQueueAction({ busy: true, error: '' }));
    try {
      const review = await dispatch(
        tenantApi.endpoints.advanceMergeQueue.initiate({ tenant, targetBranch }),
      ).unwrap();
      dispatch(patchMergeQueueAction({ busy: false, confirming: false, error: '' }));
      dispatch(
        showNotification('success', `Advanced ${review.name || review.reviewId} to merged.`),
      );
    } catch (error) {
      dispatch(patchMergeQueueAction({ busy: false, error: readError(error) }));
    }
  };

import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchMergeQueueAction } from './slices/mergeQueueActionSlice';
import type { AppThunk } from './store';

// A queue head blocked by unresolved threads is a distinct "blocked" state with a
// route forward, not an error.

export const confirmAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: true, error: '' }));
};

export const cancelAdvanceMergeQueue = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ confirming: false, error: '' }));
};

// clearAdvanceMergeQueueError keeps the confirm step so the operator retries.
export const clearAdvanceMergeQueueError = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ error: '' }));
};

// tenant is explicit: the diff panel reaches this without the dashboard open.
export const submitAdvanceMergeQueue =
  (tenant: string, repository: string, targetBranch: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    tenant = tenant.trim();
    // repository is optional; the platform refuses an ambiguous queue itself.
    if (!tenant || !targetBranch) {
      dispatch(patchMergeQueueAction({ error: 'No tenant is open.' }));
      return;
    }
    dispatch(patchMergeQueueAction({ busy: true, error: '' }));
    try {
      const review = await dispatch(
        tenantApi.endpoints.advanceMergeQueue.initiate({ tenant, repository, targetBranch }),
      ).unwrap();
      if (review.blocked) {
        dispatch(
          patchMergeQueueAction({
            busy: false,
            confirming: false,
            error: '',
            blocked: true,
            blockedReviewId: review.reviewId,
            unresolvedThreads: review.unresolvedThreads ?? 0,
          }),
        );
        return;
      }
      dispatch(
        patchMergeQueueAction({ busy: false, confirming: false, error: '', blocked: false }),
      );
      dispatch(
        showNotification(
          'success',
          `Advanced ${review.name || review.reviewId} to merge — its merge-commit build is starting.`,
        ),
      );
    } catch (error) {
      dispatch(patchMergeQueueAction({ busy: false, error: readError(error) }));
    }
  };

export const beginMergeQueueOverride = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ overriding: true, overrideReason: '', overrideError: '' }));
};

export const cancelMergeQueueOverride = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ overriding: false, overrideReason: '', overrideError: '' }));
};

export const updateMergeQueueOverrideReason =
  (reason: string): AppThunk =>
  (dispatch) => {
    dispatch(patchMergeQueueAction({ overrideReason: reason }));
  };

export const clearMergeQueueOverrideError = (): AppThunk => (dispatch) => {
  dispatch(patchMergeQueueAction({ overrideError: '' }));
};

// Leaves confirming/busy/error to the ordinary advance flow.
const defaultMergeQueueActionPatch = {
  blocked: false,
  blockedReviewId: '',
  unresolvedThreads: 0,
  overriding: false,
  overrideReason: '',
};

// submitMergeQueueOverride checks the reason locally too, to skip a round trip.
export const submitMergeQueueOverride =
  (repository: string, targetBranch: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const { tenant } = getState().tenantDashboard;
    const reason = getState().mergeQueueAction.overrideReason.trim();
    if (!tenant.trim() || !targetBranch) {
      dispatch(patchMergeQueueAction({ overrideError: 'No tenant is open.' }));
      return;
    }
    if (!reason) {
      dispatch(
        patchMergeQueueAction({ overrideError: 'A reason is required to override the gate.' }),
      );
      return;
    }
    dispatch(patchMergeQueueAction({ overrideBusy: true, overrideError: '' }));
    try {
      const review = await dispatch(
        tenantApi.endpoints.overrideAdvanceMergeQueue.initiate({
          tenant,
          repository,
          targetBranch,
          reason,
        }),
      ).unwrap();
      dispatch(patchMergeQueueAction({ overrideBusy: false, ...defaultMergeQueueActionPatch }));
      dispatch(
        showNotification(
          'success',
          `Overrode the unresolved-thread gate and advanced ${review.name || review.reviewId} to merge.`,
        ),
      );
    } catch (error) {
      dispatch(patchMergeQueueAction({ overrideBusy: false, overrideError: readError(error) }));
    }
  };

import { tenantApi } from './api/tenantApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchMergeQueueAction } from './slices/mergeQueueActionSlice';
import type { AppThunk } from './store';

// mergeQueueThunks drives the Merge Queue panel's "Advance queue" action: an
// inline confirm step (cancel-before-commitment), then the write itself. The
// target branch is derived from the queue's own rows rather than typed by the
// operator — see mergeQueueTargetBranch in TenantDashboardPanels.tsx.
//
// A refusal because the queue head still has unresolved comment threads is
// not treated as an error: AdvanceMergeQueue reports it as a distinct
// "blocked" result (see tenantApi.ts), and this state machine renders it as
// its own named state with a route forward — resolve the threads, or (with
// permission) override with a stated reason — rather than the dead end a
// bare error string would be.

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
          `Advanced ${review.name || review.reviewId} to merge — its gate build is starting.`,
        ),
      );
    } catch (error) {
      dispatch(patchMergeQueueAction({ busy: false, error: readError(error) }));
    }
  };

// beginMergeQueueOverride opens the reason input the override requires. The
// blocked state stays visible underneath — cancelling the override leaves the
// operator exactly where "resolve the threads instead" is still the answer.
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

// defaultMergeQueueActionPatch clears every blocked/override field a
// successful override leaves stale, without touching confirming/busy/error —
// those belong to the ordinary advance flow, not this one.
const defaultMergeQueueActionPatch = {
  blocked: false,
  blockedReviewId: '',
  unresolvedThreads: 0,
  overriding: false,
  overrideReason: '',
};

// submitMergeQueueOverride bypasses the gate submitAdvanceMergeQueue's
// "blocked" result reports. The platform itself refuses a blank reason, but
// this checks first so the operator sees that immediately rather than after a
// round trip.
export const submitMergeQueueOverride =
  (targetBranch: string): AppThunk<Promise<void>> =>
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
        tenantApi.endpoints.overrideAdvanceMergeQueue.initiate({ tenant, targetBranch, reason }),
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

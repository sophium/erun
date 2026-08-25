import type { UIReviewDetailInput, UITenant } from '@/types';

import { reviewDetailApi } from './api/reviewDetailApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchReviewDetail } from './slices/reviewDetailSlice';
import type { AppThunk } from './store';

// reviewDetailInput resolves the same tenant API URL + cloud alias the
// tenant dashboard itself already loaded, so opening a review's detail needs
// no input beyond the review id.
function reviewDetailInput(
  tenant: string,
  apiUrl: string,
  tenants: UITenant[],
  reviewId: string,
): UIReviewDetailInput | null {
  const cloudProviderAlias = tenants
    .find((candidate) => candidate.name === tenant)
    ?.primaryCloudProviderAlias?.trim();
  if (!apiUrl.trim() || !cloudProviderAlias) {
    return null;
  }
  return { tenant, apiUrl, cloudProviderAlias, reviewId };
}

export const openReviewDetail =
  (reviewId: string): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(
      patchReviewDetail({
        open: true,
        reviewId,
        loading: true,
        error: '',
        data: null,
        replyingTo: '',
        draftBody: '',
        submitError: '',
      }),
    );
    await dispatch(loadReviewDetail(reviewId));
  };

// closeReviewDetail hides the dialog but keeps the review as the diff
// panel's active commenting context (reviewId/data), so closing it to browse
// the diff and start a new thread from a line does not lose which review
// that thread belongs to. Only the dialog's own transient UI state resets.
export const closeReviewDetail = (): AppThunk => (dispatch) => {
  dispatch(
    patchReviewDetail({
      open: false,
      replyingTo: '',
      draftBody: '',
      submitError: '',
      closeConfirming: false,
      closeError: '',
      newCommentAnchor: null,
      newCommentDraft: '',
      newCommentSubmitError: '',
    }),
  );
};

export const loadReviewDetail =
  (reviewId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState();
    const { tenant, data } = state.tenantDashboard;
    const input = reviewDetailInput(tenant, data?.apiUrl ?? '', state.tenants.tenants, reviewId);
    if (!input) {
      dispatch(
        patchReviewDetail({
          loading: false,
          error: 'Review detail requires an API URL and a primary cloud alias.',
        }),
      );
      return;
    }
    dispatch(patchReviewDetail({ loading: true, error: '' }));
    const request = dispatch(
      reviewDetailApi.endpoints.getReviewDetail.initiate(input, { forceRefetch: true }),
    );
    try {
      const data = await request.unwrap();
      if (getState().reviewDetail.reviewId !== reviewId) {
        return;
      }
      dispatch(patchReviewDetail({ loading: false, error: '', data }));
    } catch (error) {
      if (getState().reviewDetail.reviewId !== reviewId) {
        return;
      }
      dispatch(patchReviewDetail({ loading: false, error: readError(error), data: null }));
    } finally {
      request.unsubscribe();
    }
  };

export const setReviewReplyTarget =
  (commentId: string): AppThunk =>
  (dispatch) => {
    dispatch(patchReviewDetail({ replyingTo: commentId, draftBody: '', submitError: '' }));
  };

export const cancelReviewReply = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ replyingTo: '', draftBody: '', submitError: '' }));
};

export const setReviewReplyDraft =
  (body: string): AppThunk =>
  (dispatch) => {
    dispatch(patchReviewDetail({ draftBody: body }));
  };

export const submitReviewReply = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId, replyingTo, draftBody, data } = state.reviewDetail;
  const body = draftBody.trim();
  const parent = data?.comments?.find((comment) => comment.commentId === replyingTo);
  if (!body || !parent) {
    return;
  }
  const { tenant, data: dashboardData } = state.tenantDashboard;
  const input = reviewDetailInput(
    tenant,
    dashboardData?.apiUrl ?? '',
    state.tenants.tenants,
    reviewId,
  );
  if (!input) {
    dispatch(
      patchReviewDetail({ submitError: 'Reply requires an API URL and a primary cloud alias.' }),
    );
    return;
  }
  dispatch(patchReviewDetail({ submitting: true, submitError: '' }));
  try {
    await dispatch(
      reviewDetailApi.endpoints.createReviewReply.initiate({
        tenant: input.tenant,
        apiUrl: input.apiUrl,
        cloudProviderAlias: input.cloudProviderAlias,
        reviewId,
        parentCommentId: parent.commentId,
        commitId: parent.commitId,
        filePath: parent.filePath,
        line: parent.line,
        body,
      }),
    ).unwrap();
    // Clear the draft only once the reply is durably saved — a failed submit
    // must keep it so the operator never loses what they typed.
    dispatch(
      patchReviewDetail({ submitting: false, replyingTo: '', draftBody: '', submitError: '' }),
    );
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(patchReviewDetail({ submitting: false, submitError: readError(error) }));
  }
};

// confirmCloseReview/cancelCloseReview give Close a visible commitment
// boundary: the operator sees the confirm step before the write fires and can
// back out of it, matching every other side-effecting dashboard action.
export const confirmCloseReview = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ closeConfirming: true, closeError: '' }));
};

export const cancelCloseReview = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ closeConfirming: false, closeError: '' }));
};

export const submitCloseReview = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId } = state.reviewDetail;
  const { tenant, data: dashboardData } = state.tenantDashboard;
  const input = reviewDetailInput(
    tenant,
    dashboardData?.apiUrl ?? '',
    state.tenants.tenants,
    reviewId,
  );
  if (!input) {
    dispatch(
      patchReviewDetail({ closeError: 'Closing requires an API URL and a primary cloud alias.' }),
    );
    return;
  }
  dispatch(patchReviewDetail({ closing: true, closeError: '' }));
  try {
    const review = await dispatch(
      reviewDetailApi.endpoints.closeReview.initiate({
        tenant: input.tenant,
        apiUrl: input.apiUrl,
        cloudProviderAlias: input.cloudProviderAlias,
        reviewId,
      }),
    ).unwrap();
    dispatch(patchReviewDetail({ closing: false, closeConfirming: false, closeError: '' }));
    dispatch(showNotification('success', `Closed ${review.name || review.reviewId}.`));
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(patchReviewDetail({ closing: false, closeError: readError(error) }));
  }
};

// startReviewComment/cancelReviewComment/setReviewCommentDraft/
// submitReviewComment mirror the reply flow above, but for opening a brand
// new top-level thread anchored to a diff line the operator clicked — the
// gap ReviewDetailDialog.Comments.tsx used to call out as deferred.
export const startReviewComment =
  (anchor: { commitId: string; filePath: string; line: number }): AppThunk =>
  (dispatch) => {
    dispatch(
      patchReviewDetail({
        newCommentAnchor: anchor,
        newCommentDraft: '',
        newCommentSubmitError: '',
      }),
    );
  };

export const cancelReviewComment = (): AppThunk => (dispatch) => {
  dispatch(
    patchReviewDetail({ newCommentAnchor: null, newCommentDraft: '', newCommentSubmitError: '' }),
  );
};

export const setReviewCommentDraft =
  (body: string): AppThunk =>
  (dispatch) => {
    dispatch(patchReviewDetail({ newCommentDraft: body }));
  };

export const submitReviewComment = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId, newCommentAnchor, newCommentDraft } = state.reviewDetail;
  const body = newCommentDraft.trim();
  if (!body || !newCommentAnchor) {
    return;
  }
  const { tenant, data: dashboardData } = state.tenantDashboard;
  const input = reviewDetailInput(
    tenant,
    dashboardData?.apiUrl ?? '',
    state.tenants.tenants,
    reviewId,
  );
  if (!input) {
    dispatch(
      patchReviewDetail({
        newCommentSubmitError: 'Commenting requires an API URL and a primary cloud alias.',
      }),
    );
    return;
  }
  dispatch(patchReviewDetail({ newCommentSubmitting: true, newCommentSubmitError: '' }));
  try {
    await dispatch(
      reviewDetailApi.endpoints.createReviewComment.initiate({
        tenant: input.tenant,
        apiUrl: input.apiUrl,
        cloudProviderAlias: input.cloudProviderAlias,
        reviewId,
        commitId: newCommentAnchor.commitId,
        filePath: newCommentAnchor.filePath,
        line: newCommentAnchor.line,
        body,
      }),
    ).unwrap();
    dispatch(
      patchReviewDetail({
        newCommentSubmitting: false,
        newCommentAnchor: null,
        newCommentDraft: '',
        newCommentSubmitError: '',
      }),
    );
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(
      patchReviewDetail({ newCommentSubmitting: false, newCommentSubmitError: readError(error) }),
    );
  }
};

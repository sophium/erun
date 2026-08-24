import type { UIReviewDetailInput, UITenant } from '@/types';

import { reviewDetailApi } from './api/reviewDetailApi';
import { readError } from './errors';
import { patchReviewDetail, resetReviewDetail } from './slices/reviewDetailSlice';
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

export const closeReviewDetail = (): AppThunk => (dispatch) => {
  dispatch(resetReviewDetail());
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

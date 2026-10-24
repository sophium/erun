import type { ReviewDetailState, TenantDashboardState } from '@/app/state';

import { reviewDetailApi } from './api/reviewDetailApi';
import { readError } from './errors';
import { showNotification } from './notificationThunks';
import { patchReviewDetail } from './slices/reviewDetailSlice';
import { setReviewUnresolvedThreads } from './slices/tenantDashboardSlice';
import type { AppThunk } from './store';
import { reviewDetailUnresolvedThreads } from './tenantDashboardPanels';

// ReviewCallerContext: the platform is resolved server-side from the tenant; the
// alias is only for signing in after a stale-identity failure.
interface ReviewCallerContext {
  tenant: string;
  platformAlias: string;
}

// reviewCallerContext prefers the loaded review's fields, which outlive the tenant
// dashboard.
function reviewCallerContext(
  state: { reviewDetail: ReviewDetailState; tenantDashboard: TenantDashboardState },
  reviewId: string,
): ReviewCallerContext | null {
  const detail = state.reviewDetail;
  if (detail.reviewId === reviewId && detail.callerTenant) {
    return { tenant: detail.callerTenant, platformAlias: detail.callerPlatformAlias };
  }
  const tenant = state.tenantDashboard.tenant.trim();
  if (!tenant) {
    return null;
  }
  return { tenant, platformAlias: state.tenantDashboard.data?.platformAlias ?? '' };
}

// callerTenant is passed because the diff panel can reach this without the tenant
// dashboard ever loading.
export const openReviewDetail =
  (reviewId: string, callerTenant = ''): AppThunk<Promise<void>> =>
  async (dispatch) => {
    dispatch(
      patchReviewDetail({
        open: true,
        reviewId,
        loading: true,
        error: '',
        data: null,
        callerTenant,
        callerPlatformAlias: '',
        replyingTo: '',
        draftBody: '',
        submitError: '',
      }),
    );
    await dispatch(loadReviewDetail(reviewId));
  };

// closeReviewDetail keeps the review as the diff panel's commenting context.
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
    const context = reviewCallerContext(getState(), reviewId);
    if (!context) {
      dispatch(patchReviewDetail({ loading: false, error: 'No tenant is open.' }));
      return;
    }
    dispatch(patchReviewDetail({ loading: true, error: '' }));
    const request = dispatch(
      reviewDetailApi.endpoints.getReviewDetail.initiate(
        { tenant: context.tenant, reviewId },
        { forceRefetch: true },
      ),
    );
    try {
      const data = await request.unwrap();
      if (getState().reviewDetail.reviewId !== reviewId) {
        return;
      }
      dispatch(
        patchReviewDetail({
          loading: false,
          error: '',
          data,
          callerTenant: context.tenant,
          callerPlatformAlias: context.platformAlias,
        }),
      );
      // The detail's count is authoritative; write it back so the list agrees.
      const unresolvedThreads = reviewDetailUnresolvedThreads(data);
      if (unresolvedThreads !== undefined) {
        dispatch(setReviewUnresolvedThreads({ reviewId, unresolvedThreads }));
      }
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

export const clearReviewReplyError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ submitError: '' }));
};

export const submitReviewReply = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId, replyingTo, draftBody, data } = state.reviewDetail;
  const body = draftBody.trim();
  const parent = data?.comments?.find((comment) => comment.commentId === replyingTo);
  if (!body || !parent) {
    return;
  }
  const context = reviewCallerContext(state, reviewId);
  if (!context) {
    dispatch(patchReviewDetail({ submitError: 'No tenant is open.' }));
    return;
  }
  dispatch(patchReviewDetail({ submitting: true, submitError: '' }));
  try {
    await dispatch(
      reviewDetailApi.endpoints.createReviewReply.initiate({
        tenant: context.tenant,
        reviewId,
        parentCommentId: parent.commentId,
        commitId: parent.commitId,
        filePath: parent.filePath,
        line: parent.line,
        body,
      }),
    ).unwrap();
    // Clear only once saved, so a failed submit keeps the draft.
    dispatch(
      patchReviewDetail({ submitting: false, replyingTo: '', draftBody: '', submitError: '' }),
    );
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(patchReviewDetail({ submitting: false, submitError: readError(error) }));
  }
};

export const confirmCloseReview = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ closeConfirming: true, closeError: '' }));
};

export const cancelCloseReview = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ closeConfirming: false, closeError: '' }));
};

// clearCloseReviewError keeps the panel in place so the operator retries Close.
export const clearCloseReviewError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ closeError: '' }));
};

export const submitCloseReview = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId } = state.reviewDetail;
  const context = reviewCallerContext(state, reviewId);
  if (!context) {
    dispatch(patchReviewDetail({ closeError: 'No tenant is open.' }));
    return;
  }
  dispatch(patchReviewDetail({ closing: true, closeError: '' }));
  try {
    const review = await dispatch(
      reviewDetailApi.endpoints.closeReview.initiate({ tenant: context.tenant, reviewId }),
    ).unwrap();
    dispatch(patchReviewDetail({ closing: false, closeConfirming: false, closeError: '' }));
    dispatch(showNotification('success', `Closed ${review.name || review.reviewId}.`));
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(patchReviewDetail({ closing: false, closeError: readError(error) }));
  }
};

// The dialog offers resolve only on a thread root, so there is no reply rejection.
export const submitResolveComment = (commentId: string): AppThunk<Promise<void>> =>
  submitCommentStatus(commentId, 'resolveReviewComment');

export const submitUnresolveComment = (commentId: string): AppThunk<Promise<void>> =>
  submitCommentStatus(commentId, 'unresolveReviewComment');

export const clearResolveCommentError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ resolveError: '', resolveErrorCommentId: '' }));
};

function submitCommentStatus(
  commentId: string,
  endpoint: 'resolveReviewComment' | 'unresolveReviewComment',
): AppThunk<Promise<void>> {
  return async (dispatch, getState) => {
    const state = getState();
    const { reviewId } = state.reviewDetail;
    const context = reviewCallerContext(state, reviewId);
    if (!context) {
      dispatch(
        patchReviewDetail({ resolveError: 'No tenant is open.', resolveErrorCommentId: commentId }),
      );
      return;
    }
    dispatch(
      patchReviewDetail({
        resolvingCommentId: commentId,
        resolveError: '',
        resolveErrorCommentId: '',
      }),
    );
    try {
      await dispatch(
        reviewDetailApi.endpoints[endpoint].initiate({
          tenant: context.tenant,
          reviewId,
          commentId,
        }),
      ).unwrap();
      dispatch(
        patchReviewDetail({ resolvingCommentId: '', resolveError: '', resolveErrorCommentId: '' }),
      );
      await dispatch(loadReviewDetail(reviewId));
    } catch (error) {
      dispatch(
        patchReviewDetail({
          resolvingCommentId: '',
          resolveError: readError(error),
          resolveErrorCommentId: commentId,
        }),
      );
    }
  };
}

export const startAddReviewer = (): AppThunk => (dispatch) => {
  dispatch(
    patchReviewDetail({ addReviewerOpen: true, addReviewerUserId: '', addReviewerError: '' }),
  );
};

export const cancelAddReviewer = (): AppThunk => (dispatch) => {
  dispatch(
    patchReviewDetail({ addReviewerOpen: false, addReviewerUserId: '', addReviewerError: '' }),
  );
};

export const setAddReviewerUserId =
  (userId: string): AppThunk =>
  (dispatch) => {
    dispatch(patchReviewDetail({ addReviewerUserId: userId }));
  };

export const clearAddReviewerError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ addReviewerError: '' }));
};

export const submitAddReviewer = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId, addReviewerUserId } = state.reviewDetail;
  const userId = addReviewerUserId.trim();
  if (!userId) {
    return;
  }
  const context = reviewCallerContext(state, reviewId);
  if (!context) {
    dispatch(patchReviewDetail({ addReviewerError: 'No tenant is open.' }));
    return;
  }
  dispatch(patchReviewDetail({ addReviewerSubmitting: true, addReviewerError: '' }));
  try {
    await dispatch(
      reviewDetailApi.endpoints.addReviewer.initiate({ tenant: context.tenant, reviewId, userId }),
    ).unwrap();
    dispatch(
      patchReviewDetail({
        addReviewerSubmitting: false,
        addReviewerOpen: false,
        addReviewerUserId: '',
        addReviewerError: '',
      }),
    );
    await dispatch(loadReviewDetail(reviewId));
  } catch (error) {
    dispatch(
      patchReviewDetail({ addReviewerSubmitting: false, addReviewerError: readError(error) }),
    );
  }
};

export const confirmRemoveReviewer =
  (userId: string): AppThunk =>
  (dispatch) => {
    dispatch(
      patchReviewDetail({ removeReviewerConfirmingUserId: userId, removeReviewerError: '' }),
    );
  };

export const cancelRemoveReviewer = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ removeReviewerConfirmingUserId: '', removeReviewerError: '' }));
};

export const clearRemoveReviewerError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ removeReviewerError: '', removeReviewerErrorUserId: '' }));
};

export const submitRemoveReviewer =
  (userId: string): AppThunk<Promise<void>> =>
  async (dispatch, getState) => {
    const state = getState();
    const { reviewId } = state.reviewDetail;
    const context = reviewCallerContext(state, reviewId);
    if (!context) {
      dispatch(
        patchReviewDetail({
          removeReviewerError: 'No tenant is open.',
          removeReviewerErrorUserId: userId,
        }),
      );
      return;
    }
    dispatch(
      patchReviewDetail({
        removingReviewerId: userId,
        removeReviewerConfirmingUserId: '',
        removeReviewerError: '',
        removeReviewerErrorUserId: '',
      }),
    );
    try {
      await dispatch(
        reviewDetailApi.endpoints.removeReviewer.initiate({
          tenant: context.tenant,
          reviewId,
          userId,
        }),
      ).unwrap();
      dispatch(
        patchReviewDetail({
          removingReviewerId: '',
          removeReviewerError: '',
          removeReviewerErrorUserId: '',
        }),
      );
      await dispatch(loadReviewDetail(reviewId));
    } catch (error) {
      dispatch(
        patchReviewDetail({
          removingReviewerId: '',
          removeReviewerError: readError(error),
          removeReviewerErrorUserId: userId,
        }),
      );
    }
  };

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

export const clearReviewCommentError = (): AppThunk => (dispatch) => {
  dispatch(patchReviewDetail({ newCommentSubmitError: '' }));
};

export const submitReviewComment = (): AppThunk<Promise<void>> => async (dispatch, getState) => {
  const state = getState();
  const { reviewId, newCommentAnchor, newCommentDraft } = state.reviewDetail;
  const body = newCommentDraft.trim();
  if (!body || !newCommentAnchor) {
    return;
  }
  const context = reviewCallerContext(state, reviewId);
  if (!context) {
    dispatch(patchReviewDetail({ newCommentSubmitError: 'No tenant is open.' }));
    return;
  }
  dispatch(patchReviewDetail({ newCommentSubmitting: true, newCommentSubmitError: '' }));
  try {
    await dispatch(
      reviewDetailApi.endpoints.createReviewComment.initiate({
        tenant: context.tenant,
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

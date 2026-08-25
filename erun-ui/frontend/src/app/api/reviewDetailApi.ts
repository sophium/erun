import type {
  UICloseReviewInput,
  UICreateReviewCommentInput,
  UICreateReviewReplyInput,
  UIReviewComment,
  UIReviewDetail,
  UIReviewDetailInput,
  UITenantDashboardReview,
} from '@/types';

import {
  CloseReview,
  CreateReviewComment,
  CreateReviewReply,
  LoadReviewDetail,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

// reviewDetailApi is named apart from reviewApi (the local diff panel's own
// endpoint) on purpose: "review" already means the diff panel here, and this
// is the hosted platform review object the Reviews tab shows.
export const reviewDetailApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getReviewDetail: builder.query<UIReviewDetail, UIReviewDetailInput>({
      queryFn: wailsQueryFn<UIReviewDetailInput, UIReviewDetail>((input) =>
        LoadReviewDetail(input),
      ),
      providesTags: (_result, _error, input) => [{ type: 'ReviewDetail', id: input.reviewId }],
    }),
    createReviewReply: builder.mutation<UIReviewComment, UICreateReviewReplyInput>({
      queryFn: wailsQueryFn<UICreateReviewReplyInput, UIReviewComment>((input) =>
        CreateReviewReply(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'ReviewDetail', id: input.reviewId }],
    }),
    createReviewComment: builder.mutation<UIReviewComment, UICreateReviewCommentInput>({
      queryFn: wailsQueryFn<UICreateReviewCommentInput, UIReviewComment>((input) =>
        CreateReviewComment(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'ReviewDetail', id: input.reviewId }],
    }),
    closeReview: builder.mutation<UITenantDashboardReview, UICloseReviewInput>({
      queryFn: wailsQueryFn<UICloseReviewInput, UITenantDashboardReview>((input) =>
        CloseReview(input),
      ),
      invalidatesTags: (_result, _error, input) => [
        { type: 'ReviewDetail', id: input.reviewId },
        { type: 'TenantDashboard', id: input.tenant },
      ],
    }),
  }),
});

export const {
  useGetReviewDetailQuery,
  useLazyGetReviewDetailQuery,
  useCreateReviewReplyMutation,
  useCreateReviewCommentMutation,
  useCloseReviewMutation,
} = reviewDetailApi;

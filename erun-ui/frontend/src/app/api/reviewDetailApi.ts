import type {
  UICreateReviewReplyInput,
  UIReviewComment,
  UIReviewDetail,
  UIReviewDetailInput,
} from '@/types';

import { CreateReviewReply, LoadReviewDetail } from '../../../wailsjs/go/main/App';
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
  }),
});

export const {
  useGetReviewDetailQuery,
  useLazyGetReviewDetailQuery,
  useCreateReviewReplyMutation,
} = reviewDetailApi;

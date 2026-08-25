import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultReviewDetail, type ReviewDetailState } from '../state';

const initialState: ReviewDetailState = defaultReviewDetail();

export const reviewDetailSlice = createSlice({
  name: 'reviewDetail',
  initialState,
  reducers: {
    patchReviewDetail(state, action: PayloadAction<Partial<ReviewDetailState>>) {
      Object.assign(state, action.payload);
    },
  },
});

export const { patchReviewDetail } = reviewDetailSlice.actions;
export default reviewDetailSlice.reducer;

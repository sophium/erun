import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import { defaultTenantDashboard, type TenantDashboardState } from '../state';

const initialState: TenantDashboardState = defaultTenantDashboard();

export const tenantDashboardSlice = createSlice({
  name: 'tenantDashboard',
  initialState,
  reducers: {
    setTenantDashboard(_state, action: PayloadAction<TenantDashboardState>) {
      return action.payload;
    },
    patchTenantDashboard(state, action: PayloadAction<Partial<TenantDashboardState>>) {
      Object.assign(state, action.payload);
    },
    // setReviewUnresolvedThreads writes back the count a review detail read
    // computed from that review's own comment threads, so the list row and
    // the dialog over it report the same number. The detail's read is the
    // authoritative one: it holds the threads. A review the loaded dashboard
    // does not list (or a dashboard that never loaded) is left alone rather
    // than invented.
    setReviewUnresolvedThreads(
      state,
      action: PayloadAction<{ reviewId: string; unresolvedThreads: number }>,
    ) {
      const review = state.data?.reviews?.find((row) => row.reviewId === action.payload.reviewId);
      if (review) {
        review.unresolvedThreads = action.payload.unresolvedThreads;
      }
    },
    resetTenantDashboard() {
      return defaultTenantDashboard();
    },
  },
});

export const {
  setTenantDashboard,
  patchTenantDashboard,
  resetTenantDashboard,
  setReviewUnresolvedThreads,
} = tenantDashboardSlice.actions;
export default tenantDashboardSlice.reducer;

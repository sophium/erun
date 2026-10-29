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
    // The review detail's thread count is authoritative; unlisted reviews are left alone.
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

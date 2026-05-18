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
    resetTenantDashboard() {
      return defaultTenantDashboard();
    },
  },
});

export const { setTenantDashboard, patchTenantDashboard, resetTenantDashboard } =
  tenantDashboardSlice.actions;
export default tenantDashboardSlice.reducer;

import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type { UICloudProviderStatus, UITenant, UIVersionSuggestion } from '@/types';

export interface TenantsState {
  tenants: UITenant[];
  cloudProviders: UICloudProviderStatus[];
  versionSuggestions: UIVersionSuggestion[];
}

const initialState: TenantsState = {
  tenants: [],
  cloudProviders: [],
  versionSuggestions: [],
};

export const tenantsSlice = createSlice({
  name: 'tenants',
  initialState,
  reducers: {
    setTenants(state, action: PayloadAction<UITenant[]>) {
      state.tenants = action.payload;
    },
    setCloudProviders(state, action: PayloadAction<UICloudProviderStatus[]>) {
      state.cloudProviders = action.payload;
    },
    setVersionSuggestions(state, action: PayloadAction<UIVersionSuggestion[]>) {
      state.versionSuggestions = action.payload;
    },
  },
});

export const {
  setTenants,
  setCloudProviders,
  setVersionSuggestions,
} = tenantsSlice.actions;
export default tenantsSlice.reducer;

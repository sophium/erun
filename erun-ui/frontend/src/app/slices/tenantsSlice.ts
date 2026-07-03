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
    // Optimistically reflect a persisted AutoStart change so the next env-open
    // uses the new policy without waiting for a full state reload.
    patchTenantEnvironmentAutoStart(
      state,
      action: PayloadAction<{
        tenant: string;
        environment: string;
        autoStart: boolean | undefined;
      }>,
    ) {
      const tenant = state.tenants.find((item) => item.name === action.payload.tenant);
      if (!tenant) {
        return;
      }
      const env = tenant.environments.find((item) => item.name === action.payload.environment);
      if (!env) {
        return;
      }
      env.autoStart = action.payload.autoStart;
    },
  },
});

export const {
  setTenants,
  setCloudProviders,
  setVersionSuggestions,
  patchTenantEnvironmentAutoStart,
} = tenantsSlice.actions;
export default tenantsSlice.reducer;

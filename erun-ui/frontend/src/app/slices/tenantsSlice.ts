import { createSlice, type PayloadAction } from '@reduxjs/toolkit';

import type {
  UICloudProviderStatus,
  UITenant,
  UIVersionSuggestion,
  UIVersionSuggestionNotice,
} from '@/types';

export interface TenantsState {
  tenants: UITenant[];
  // Distinguishes "not loaded yet" from "genuinely empty".
  tenantsLoaded: boolean;
  cloudProviders: UICloudProviderStatus[];
  versionSuggestions: UIVersionSuggestion[];
  versionSuggestionNotices: UIVersionSuggestionNotice[];
}

const initialState: TenantsState = {
  tenants: [],
  tenantsLoaded: false,
  cloudProviders: [],
  versionSuggestions: [],
  versionSuggestionNotices: [],
};

export const tenantsSlice = createSlice({
  name: 'tenants',
  initialState,
  reducers: {
    setTenants(state, action: PayloadAction<UITenant[]>) {
      state.tenants = action.payload;
      state.tenantsLoaded = true;
    },
    setCloudProviders(state, action: PayloadAction<UICloudProviderStatus[]>) {
      state.cloudProviders = action.payload;
    },
    setVersionSuggestions(state, action: PayloadAction<UIVersionSuggestion[]>) {
      state.versionSuggestions = action.payload;
    },
    setVersionSuggestionNotices(state, action: PayloadAction<UIVersionSuggestionNotice[]>) {
      state.versionSuggestionNotices = action.payload;
    },
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
  setVersionSuggestionNotices,
  patchTenantEnvironmentAutoStart,
} = tenantsSlice.actions;
export default tenantsSlice.reducer;

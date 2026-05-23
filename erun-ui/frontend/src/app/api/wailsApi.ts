import { createApi } from '@reduxjs/toolkit/query/react';

import { wailsBaseQuery } from './wailsBaseQuery';

// Single createApi instance keeps a unified cache, tag namespace, and
// reducer slot. Per-domain modules add endpoints via injectEndpoints.
export const wailsApi = createApi({
  reducerPath: 'wailsApi',
  baseQuery: wailsBaseQuery,
  tagTypes: [
    'AppState',
    'EnvironmentConfig',
    'TenantConfig',
    'TenantDashboard',
    'GlobalConfig',
    'KubernetesContexts',
    'RuntimeResourceStatus',
    'VersionSuggestions',
    'CloudContexts',
    'CloudContextApiStop',
    'CloudProviders',
    'Diff',
    'IdleStatus',
    'Deploys',
  ],
  endpoints: () => ({}),
});

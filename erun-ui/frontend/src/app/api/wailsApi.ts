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
    'RuntimeActivity',
    'RuntimeUsage',
    'RuntimeSizing',
    'VersionSuggestions',
    'CloudContexts',
    'CloudContextApiStop',
    'CloudProviders',
    'Diff',
    'IdleStatus',
    'Deploys',
    'ReviewDetail',
  ],
  endpoints: () => ({}),
});

// Lets a domain module (environmentApi.ts) split its endpoint map into a
// helper function without hand-rolling wailsApi's generic EndpointBuilder
// instantiation.
export type EnvironmentApiBuilder = Parameters<
  Parameters<typeof wailsApi.injectEndpoints>[0]['endpoints']
>[0];

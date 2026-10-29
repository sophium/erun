import { createApi } from '@reduxjs/toolkit/query/react';

import { wailsBaseQuery } from './wailsBaseQuery';

// One createApi instance; domain modules use injectEndpoints.
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
    'RuntimeRunState',
    'RuntimeSizing',
    'VersionSuggestions',
    'CloudContexts',
    'CloudContextApiStop',
    'CloudProviders',
    'Diff',
    'IdleStatus',
    'Deploys',
    'ReviewDetail',
    'TenantEnrollment',
  ],
  endpoints: () => ({}),
});

export type EnvironmentApiBuilder = Parameters<
  Parameters<typeof wailsApi.injectEndpoints>[0]['endpoints']
>[0];

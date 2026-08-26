import type {
  UIAdvanceMergeQueueInput,
  UICloudProviderStatus,
  UIConnectERunPlatformInput,
  UICreateReviewInput,
  UIOverrideAdvanceMergeQueueInput,
  UIPlatformUser,
  UIPlatformUserEnrollInput,
  UITenantConfig,
  UITenantDashboard,
  UITenantDashboardInput,
  UITenantDashboardReview,
} from '@/types';

import {
  AdvanceMergeQueue,
  ConnectERunPlatform,
  CreateReview,
  EnrollERunPlatformUser,
  LoadTenantConfig,
  LoadTenantDashboard,
  OverrideAdvanceMergeQueue,
  SaveTenantConfig,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const tenantApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getTenantConfig: builder.query<UITenantConfig, string>({
      queryFn: wailsQueryFn<string, UITenantConfig>((tenant) => LoadTenantConfig(tenant)),
      providesTags: (_result, _error, tenant) => [{ type: 'TenantConfig', id: tenant }],
    }),
    saveTenantConfig: builder.mutation<UITenantConfig, UITenantConfig>({
      queryFn: wailsQueryFn<UITenantConfig, UITenantConfig>((config) =>
        SaveTenantConfig(config as never),
      ),
      invalidatesTags: (_result, _error, config) => [
        { type: 'TenantConfig', id: config.name },
        'AppState',
      ],
    }),
    getTenantDashboard: builder.query<UITenantDashboard, UITenantDashboardInput>({
      queryFn: wailsQueryFn<UITenantDashboardInput, UITenantDashboard>((input) =>
        LoadTenantDashboard(input),
      ),
      providesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    createReview: builder.mutation<UITenantDashboardReview, UICreateReviewInput>({
      queryFn: wailsQueryFn<UICreateReviewInput, UITenantDashboardReview>((input) =>
        CreateReview(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    advanceMergeQueue: builder.mutation<UITenantDashboardReview, UIAdvanceMergeQueueInput>({
      queryFn: wailsQueryFn<UIAdvanceMergeQueueInput, UITenantDashboardReview>((input) =>
        AdvanceMergeQueue(input),
      ),
      // A blocked result changed nothing on the platform, so there is nothing
      // to refetch; only a real promotion invalidates the dashboard.
      invalidatesTags: (result, _error, input) =>
        result && !result.blocked ? [{ type: 'TenantDashboard', id: input.tenant }] : [],
    }),
    overrideAdvanceMergeQueue: builder.mutation<
      UITenantDashboardReview,
      UIOverrideAdvanceMergeQueueInput
    >({
      queryFn: wailsQueryFn<UIOverrideAdvanceMergeQueueInput, UITenantDashboardReview>((input) =>
        OverrideAdvanceMergeQueue(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    connectERunPlatform: builder.mutation<UICloudProviderStatus, UIConnectERunPlatformInput>({
      queryFn: wailsQueryFn<UIConnectERunPlatformInput, UICloudProviderStatus>((input) =>
        ConnectERunPlatform(input),
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    enrollERunPlatformUser: builder.mutation<UIPlatformUser, UIPlatformUserEnrollInput>({
      queryFn: wailsQueryFn<UIPlatformUserEnrollInput, UIPlatformUser>((input) =>
        EnrollERunPlatformUser(input),
      ),
    }),
  }),
});

export const {
  useGetTenantConfigQuery,
  useLazyGetTenantConfigQuery,
  useSaveTenantConfigMutation,
  useGetTenantDashboardQuery,
  useLazyGetTenantDashboardQuery,
  useCreateReviewMutation,
  useAdvanceMergeQueueMutation,
  useOverrideAdvanceMergeQueueMutation,
  useConnectERunPlatformMutation,
  useEnrollERunPlatformUserMutation,
} = tenantApi;

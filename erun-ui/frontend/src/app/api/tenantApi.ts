import type {
  UIAdvanceMergeQueueInput,
  UICloudProviderStatus,
  UIConnectERunPlatformInput,
  UICreatePlatformContextInput,
  UICreateReviewInput,
  UIOverrideAdvanceMergeQueueInput,
  UIPlatformContextOutcome,
  UIPlatformEnvironmentActionInput,
  UIPlatformEnvironmentOutcome,
  UIPlatformProvisionResult,
  UIPlatformUser,
  UIPlatformUserEnrollInput,
  UIRegisterPlatformEnvironmentInput,
  UITenantConfig,
  UITenantDashboard,
  UITenantDashboardInput,
  UITenantDashboardReview,
} from '@/types';

import {
  AdvanceMergeQueue,
  ConnectERunPlatform,
  CreatePlatformContext,
  CreateReview,
  DeletePlatformEnvironment,
  DeployPlatformEnvironment,
  EnrollERunPlatformUser,
  LoadTenantConfig,
  LoadTenantDashboard,
  OverrideAdvanceMergeQueue,
  PreviewPlatformEnvironment,
  RegisterPlatformEnvironment,
  SaveTenantConfig,
  StopPlatformEnvironment,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

// previewPlatformEnvironmentEndpoint is defined outside the endpoints
// factory (rather than inlined like its neighbours) purely to keep that
// factory under eslint's max-lines-per-function cap — its own generics are
// too long to fit Prettier's line width in the inline shape every other
// endpoint here uses.
const previewPlatformEnvironmentEndpoint = {
  queryFn: wailsQueryFn<UIRegisterPlatformEnvironmentInput, UIPlatformProvisionResult>((input) =>
    PreviewPlatformEnvironment(input),
  ),
};

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
    createPlatformContext: builder.mutation<UIPlatformContextOutcome, UICreatePlatformContextInput>(
      {
        queryFn: wailsQueryFn<UICreatePlatformContextInput, UIPlatformContextOutcome>((input) =>
          CreatePlatformContext(input),
        ),
      },
    ),
    previewPlatformEnvironment: builder.mutation<
      UIPlatformProvisionResult,
      UIRegisterPlatformEnvironmentInput
    >(previewPlatformEnvironmentEndpoint),
    registerPlatformEnvironment: builder.mutation<
      UIPlatformEnvironmentOutcome,
      UIRegisterPlatformEnvironmentInput
    >({
      queryFn: wailsQueryFn<UIRegisterPlatformEnvironmentInput, UIPlatformEnvironmentOutcome>(
        (input) => RegisterPlatformEnvironment(input),
      ),
    }),
    deployPlatformEnvironment: builder.mutation<
      UIPlatformEnvironmentOutcome,
      UIPlatformEnvironmentActionInput
    >({
      queryFn: wailsQueryFn<UIPlatformEnvironmentActionInput, UIPlatformEnvironmentOutcome>(
        (input) => DeployPlatformEnvironment(input),
      ),
    }),
    stopPlatformEnvironment: builder.mutation<
      UIPlatformEnvironmentOutcome,
      UIPlatformEnvironmentActionInput
    >({
      queryFn: wailsQueryFn<UIPlatformEnvironmentActionInput, UIPlatformEnvironmentOutcome>(
        (input) => StopPlatformEnvironment(input),
      ),
    }),
    deletePlatformEnvironment: builder.mutation<
      UIPlatformEnvironmentOutcome,
      UIPlatformEnvironmentActionInput
    >({
      queryFn: wailsQueryFn<UIPlatformEnvironmentActionInput, UIPlatformEnvironmentOutcome>(
        (input) => DeletePlatformEnvironment(input),
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
  useCreatePlatformContextMutation,
  usePreviewPlatformEnvironmentMutation,
  useRegisterPlatformEnvironmentMutation,
  useDeployPlatformEnvironmentMutation,
  useStopPlatformEnvironmentMutation,
  useDeletePlatformEnvironmentMutation,
} = tenantApi;

import type {
  UIAWSCloudAliasInput,
  UICloudContextInitInput,
  UICloudContextStatus,
  UICloudProviderBearerToken,
  UICloudProviderStatus,
} from '@/types';

import {
  DescribeCloudContextApiStop,
  DisableCloudContextApiStop,
  EnableCloudContextApiStop,
  GetCloudProviderBearerToken,
  InitAWSCloudProvider,
  InitCloudContext,
  LoadCloudContextStatuses,
  LoadCloudProviderStatuses,
  LoginCloudProvider,
  LogoutCloudProvider,
  SaveAWSCloudProviderAlias,
  SetupCloudProviderOIDC,
  StartCloudContext,
  StopCloudContext,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

export const cloudApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getCloudContextStatuses: builder.query<UICloudContextStatus[], NoValue>({
      queryFn: wailsQueryFn<NoValue, UICloudContextStatus[]>(() => LoadCloudContextStatuses()),
      providesTags: ['CloudContexts'],
    }),
    getCloudProviderStatuses: builder.query<UICloudProviderStatus[], NoValue>({
      queryFn: wailsQueryFn<NoValue, UICloudProviderStatus[]>(() => LoadCloudProviderStatuses()),
      providesTags: ['CloudProviders'],
    }),
    initCloudContext: builder.mutation<UICloudContextStatus, UICloudContextInitInput>({
      queryFn: wailsQueryFn<UICloudContextInitInput, UICloudContextStatus>((input) =>
        InitCloudContext(input),
      ),
      invalidatesTags: ['CloudContexts', 'AppState'],
    }),
    startCloudContext: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) => StartCloudContext(name)),
      invalidatesTags: ['CloudContexts'],
    }),
    stopCloudContext: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) => StopCloudContext(name)),
      invalidatesTags: ['CloudContexts'],
    }),
    getCloudContextApiStop: builder.query<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) =>
        DescribeCloudContextApiStop(name),
      ),
      providesTags: (_result, _error, name) => [{ type: 'CloudContextApiStop', id: name }],
    }),
    disableCloudContextApiStop: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) =>
        DisableCloudContextApiStop(name),
      ),
      invalidatesTags: (_result, _error, name) => [{ type: 'CloudContextApiStop', id: name }],
    }),
    enableCloudContextApiStop: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) =>
        EnableCloudContextApiStop(name),
      ),
      invalidatesTags: (_result, _error, name) => [{ type: 'CloudContextApiStop', id: name }],
    }),
    loginCloudProvider: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>((alias) => LoginCloudProvider(alias)),
      invalidatesTags: ['CloudProviders'],
    }),
    logoutCloudProvider: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>((alias) => LogoutCloudProvider(alias)),
      invalidatesTags: ['CloudProviders'],
    }),
    getCloudProviderBearerToken: builder.mutation<UICloudProviderBearerToken, string>({
      queryFn: wailsQueryFn<string, UICloudProviderBearerToken>((alias) =>
        GetCloudProviderBearerToken(alias),
      ),
    }),
    setupCloudProviderOIDC: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>((alias) =>
        SetupCloudProviderOIDC(alias),
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    initAWSCloudProvider: builder.mutation<UICloudProviderStatus, UIAWSCloudAliasInput>({
      queryFn: wailsQueryFn<UIAWSCloudAliasInput, UICloudProviderStatus>((input) =>
        InitAWSCloudProvider(input),
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    saveAWSCloudProviderAlias: builder.mutation<UICloudProviderStatus, UIAWSCloudAliasInput>({
      queryFn: wailsQueryFn<UIAWSCloudAliasInput, UICloudProviderStatus>((input) =>
        SaveAWSCloudProviderAlias(input),
      ),
      invalidatesTags: ['CloudProviders'],
    }),
  }),
});

export const {
  useGetCloudContextStatusesQuery,
  useGetCloudProviderStatusesQuery,
  useInitCloudContextMutation,
  useStartCloudContextMutation,
  useStopCloudContextMutation,
  useGetCloudContextApiStopQuery,
  useDisableCloudContextApiStopMutation,
  useEnableCloudContextApiStopMutation,
  useLoginCloudProviderMutation,
  useLogoutCloudProviderMutation,
  useGetCloudProviderBearerTokenMutation,
  useSetupCloudProviderOIDCMutation,
  useInitAWSCloudProviderMutation,
  useSaveAWSCloudProviderAliasMutation,
} = cloudApi;

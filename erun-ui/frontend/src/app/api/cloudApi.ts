import {
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
import { wailsQueryFn } from './wailsBaseQuery';
import type {
  UIAWSCloudAliasInput,
  UICloudContextInitInput,
  UICloudContextStatus,
  UICloudProviderBearerToken,
  UICloudProviderStatus,
} from '@/types';

export const cloudApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getCloudContextStatuses: builder.query<UICloudContextStatus[], void>({
      queryFn: wailsQueryFn<void, UICloudContextStatus[]>(
        () => LoadCloudContextStatuses() as Promise<UICloudContextStatus[]>,
      ),
      providesTags: ['CloudContexts'],
    }),
    getCloudProviderStatuses: builder.query<UICloudProviderStatus[], void>({
      queryFn: wailsQueryFn<void, UICloudProviderStatus[]>(
        () => LoadCloudProviderStatuses() as Promise<UICloudProviderStatus[]>,
      ),
      providesTags: ['CloudProviders'],
    }),
    initCloudContext: builder.mutation<UICloudContextStatus, UICloudContextInitInput>({
      queryFn: wailsQueryFn<UICloudContextInitInput, UICloudContextStatus>(
        (input) => InitCloudContext(input) as Promise<UICloudContextStatus>,
      ),
      invalidatesTags: ['CloudContexts', 'AppState'],
    }),
    startCloudContext: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>(
        (name) => StartCloudContext(name) as Promise<UICloudContextStatus>,
      ),
      invalidatesTags: ['CloudContexts'],
    }),
    stopCloudContext: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>(
        (name) => StopCloudContext(name) as Promise<UICloudContextStatus>,
      ),
      invalidatesTags: ['CloudContexts'],
    }),
    loginCloudProvider: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>(
        (alias) => LoginCloudProvider(alias) as Promise<UICloudProviderStatus>,
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    logoutCloudProvider: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>(
        (alias) => LogoutCloudProvider(alias) as Promise<UICloudProviderStatus>,
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    getCloudProviderBearerToken: builder.mutation<UICloudProviderBearerToken, string>({
      queryFn: wailsQueryFn<string, UICloudProviderBearerToken>(
        (alias) => GetCloudProviderBearerToken(alias) as Promise<UICloudProviderBearerToken>,
      ),
    }),
    setupCloudProviderOIDC: builder.mutation<UICloudProviderStatus, string>({
      queryFn: wailsQueryFn<string, UICloudProviderStatus>(
        (alias) => SetupCloudProviderOIDC(alias) as Promise<UICloudProviderStatus>,
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    initAWSCloudProvider: builder.mutation<UICloudProviderStatus, UIAWSCloudAliasInput>({
      queryFn: wailsQueryFn<UIAWSCloudAliasInput, UICloudProviderStatus>(
        (input) => InitAWSCloudProvider(input) as Promise<UICloudProviderStatus>,
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    saveAWSCloudProviderAlias: builder.mutation<UICloudProviderStatus, UIAWSCloudAliasInput>({
      queryFn: wailsQueryFn<UIAWSCloudAliasInput, UICloudProviderStatus>(
        (input) => SaveAWSCloudProviderAlias(input) as Promise<UICloudProviderStatus>,
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
  useLoginCloudProviderMutation,
  useLogoutCloudProviderMutation,
  useGetCloudProviderBearerTokenMutation,
  useSetupCloudProviderOIDCMutation,
  useInitAWSCloudProviderMutation,
  useSaveAWSCloudProviderAliasMutation,
} = cloudApi;

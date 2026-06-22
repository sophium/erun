import type {
  UIAWSCloudAliasInput,
  UICloudContextInitInput,
  UICloudContextStatus,
  UICloudflareCloudAliasInput,
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
  InitCloudflareCloudProvider,
  LoadCloudContextStatuses,
  LoadCloudProviderStatuses,
  LoginCloudProvider,
  LogoutCloudProvider,
  SaveAWSCloudProviderAlias,
  SaveCloudflareCloudProviderAlias,
  SetupCloudProviderOIDC,
  StartCloudContext,
  StopCloudContext,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

async function pushApiStopMutationIntoCache(
  name: string,
  api: {
    dispatch: (action: unknown) => unknown;
    queryFulfilled: Promise<{ data: UICloudContextStatus }>;
  },
): Promise<void> {
  try {
    const { data } = await api.queryFulfilled;
    api.dispatch(cloudApi.util.updateQueryData('getCloudContextApiStop', name, () => data));
  } catch {
    // Mutation failed; the toast surfaces the error and the next
    // describe-on-remount reconciles the cache.
  }
}

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
      // The mutation already returns the authoritative post-modify
      // state. An invalidatesTags refetch here would call
      // describe-instance-attribute inside AWS's eventual-consistency
      // window for the just-issued modify-instance-attribute and could
      // return the pre-modify value, flipping the lock icon back to
      // its previous state right after the success toast fired. Push
      // the mutation result into the cache directly instead; the
      // widget's refetchOnMountOrArgChange picks up any later drift.
      onQueryStarted: pushApiStopMutationIntoCache,
    }),
    enableCloudContextApiStop: builder.mutation<UICloudContextStatus, string>({
      queryFn: wailsQueryFn<string, UICloudContextStatus>((name) =>
        EnableCloudContextApiStop(name),
      ),
      onQueryStarted: pushApiStopMutationIntoCache,
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
    initCloudflareCloudProvider: builder.mutation<
      UICloudProviderStatus,
      UICloudflareCloudAliasInput
    >({
      queryFn: wailsQueryFn<UICloudflareCloudAliasInput, UICloudProviderStatus>((input) =>
        InitCloudflareCloudProvider(input),
      ),
      invalidatesTags: ['CloudProviders'],
    }),
    saveCloudflareCloudProviderAlias: builder.mutation<
      UICloudProviderStatus,
      UICloudflareCloudAliasInput
    >({
      queryFn: wailsQueryFn<UICloudflareCloudAliasInput, UICloudProviderStatus>((input) =>
        SaveCloudflareCloudProviderAlias(input),
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
  useInitCloudflareCloudProviderMutation,
  useSaveCloudflareCloudProviderAliasMutation,
} = cloudApi;

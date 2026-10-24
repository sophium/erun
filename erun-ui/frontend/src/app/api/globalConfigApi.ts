import type { UIERunConfig } from '@/types';
import type { UIGatewayCredentialStatus, UIGatewayModel } from '@/uiOpenRouterTypes';

import {
  ClearGatewayCredential,
  LoadERunConfig,
  LoadGatewayCredentialStatus,
  LoadGatewayModels,
  SaveERunConfig,
  SaveGatewayCredential,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

export const globalConfigApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getERunConfig: builder.query<UIERunConfig, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIERunConfig>(() => LoadERunConfig()),
      providesTags: ['GlobalConfig'],
    }),
    saveERunConfig: builder.mutation<UIERunConfig, UIERunConfig>({
      queryFn: wailsQueryFn<UIERunConfig, UIERunConfig>((config) =>
        SaveERunConfig(config as never),
      ),
      invalidatesTags: ['GlobalConfig', 'AppState'],
    }),
    // A mutation: the base URL may still be changing as the operator types.
    loadGatewayModels: builder.mutation<UIGatewayModel[], string>({
      queryFn: wailsQueryFn<string, UIGatewayModel[]>((baseURL) => LoadGatewayModels(baseURL)),
    }),
    // Read rather than inferred, so the catalog reports the key a deploy will use.
    getGatewayCredentialStatus: builder.query<UIGatewayCredentialStatus, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIGatewayCredentialStatus>(() =>
        LoadGatewayCredentialStatus(),
      ),
      providesTags: ['GlobalConfig'],
    }),
    saveGatewayCredential: builder.mutation<UIGatewayCredentialStatus, string>({
      queryFn: wailsQueryFn<string, UIGatewayCredentialStatus>((token) =>
        SaveGatewayCredential(token),
      ),
      invalidatesTags: ['GlobalConfig'],
    }),
    clearGatewayCredential: builder.mutation<UIGatewayCredentialStatus, NoValue>({
      queryFn: wailsQueryFn<NoValue, UIGatewayCredentialStatus>(() => ClearGatewayCredential()),
      invalidatesTags: ['GlobalConfig'],
    }),
  }),
});

export const {
  useGetERunConfigQuery,
  useLazyGetERunConfigQuery,
  useSaveERunConfigMutation,
  useLoadGatewayModelsMutation,
  useGetGatewayCredentialStatusQuery,
  useSaveGatewayCredentialMutation,
  useClearGatewayCredentialMutation,
} = globalConfigApi;

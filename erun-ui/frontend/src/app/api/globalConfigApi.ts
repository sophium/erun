import type { UIERunConfig } from '@/types';
import type { UIGatewayModel } from '@/uiOpenRouterTypes';

import { LoadERunConfig, LoadGatewayModels, SaveERunConfig } from '../../../wailsjs/go/main/App';
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
    // A mutation rather than a query: the list to fetch depends on a base URL
    // the operator may still be editing, so it is fetched on demand rather than
    // cached against a value that changes as they type.
    loadGatewayModels: builder.mutation<UIGatewayModel[], string>({
      queryFn: wailsQueryFn<string, UIGatewayModel[]>((baseURL) => LoadGatewayModels(baseURL)),
    }),
  }),
});

export const {
  useGetERunConfigQuery,
  useLazyGetERunConfigQuery,
  useSaveERunConfigMutation,
  useLoadGatewayModelsMutation,
} = globalConfigApi;

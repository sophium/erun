import type { UIERunConfig } from '@/types';

import { LoadERunConfig, SaveERunConfig } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const globalConfigApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getERunConfig: builder.query<UIERunConfig, void>({
      queryFn: wailsQueryFn<void, UIERunConfig>(() => LoadERunConfig()),
      providesTags: ['GlobalConfig'],
    }),
    saveERunConfig: builder.mutation<UIERunConfig, UIERunConfig>({
      queryFn: wailsQueryFn<UIERunConfig, UIERunConfig>((config) =>
        SaveERunConfig(config as never),
      ),
      invalidatesTags: ['GlobalConfig', 'AppState'],
    }),
  }),
});

export const { useGetERunConfigQuery, useLazyGetERunConfigQuery, useSaveERunConfigMutation } =
  globalConfigApi;

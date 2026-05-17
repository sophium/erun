import { LoadKubernetesContexts } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

export const kubernetesApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getKubernetesContexts: builder.query<string[], NoValue>({
      queryFn: wailsQueryFn<NoValue, string[]>(() => LoadKubernetesContexts()),
      providesTags: ['KubernetesContexts'],
    }),
  }),
});

export const { useGetKubernetesContextsQuery, useLazyGetKubernetesContextsQuery } = kubernetesApi;

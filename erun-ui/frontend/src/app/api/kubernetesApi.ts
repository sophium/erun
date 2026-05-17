import { LoadKubernetesContexts } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const kubernetesApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getKubernetesContexts: builder.query<string[], void>({
      queryFn: wailsQueryFn<void, string[]>(() => LoadKubernetesContexts()),
      providesTags: ['KubernetesContexts'],
    }),
  }),
});

export const { useGetKubernetesContextsQuery, useLazyGetKubernetesContextsQuery } = kubernetesApi;

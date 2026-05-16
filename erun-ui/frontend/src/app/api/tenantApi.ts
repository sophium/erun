import {
  LoadTenantConfig,
  LoadTenantDashboard,
  SaveTenantConfig,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';
import type { UITenantConfig, UITenantDashboard, UITenantDashboardInput } from '@/types';

export const tenantApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    getTenantConfig: builder.query<UITenantConfig, string>({
      queryFn: wailsQueryFn<string, UITenantConfig>(
        (tenant) => LoadTenantConfig(tenant) as Promise<UITenantConfig>,
      ),
      providesTags: (_result, _error, tenant) => [{ type: 'TenantConfig', id: tenant }],
    }),
    saveTenantConfig: builder.mutation<UITenantConfig, UITenantConfig>({
      queryFn: wailsQueryFn<UITenantConfig, UITenantConfig>(
        (config) => SaveTenantConfig(config as never) as Promise<UITenantConfig>,
      ),
      invalidatesTags: (_result, _error, config) => [
        { type: 'TenantConfig', id: config.name },
        'AppState',
      ],
    }),
    getTenantDashboard: builder.query<UITenantDashboard, UITenantDashboardInput>({
      queryFn: wailsQueryFn<UITenantDashboardInput, UITenantDashboard>(
        (input) => LoadTenantDashboard(input) as Promise<UITenantDashboard>,
      ),
      providesTags: (_result, _error, input) => [
        { type: 'TenantDashboard', id: input.tenant },
      ],
    }),
  }),
});

export const {
  useGetTenantConfigQuery,
  useLazyGetTenantConfigQuery,
  useSaveTenantConfigMutation,
  useGetTenantDashboardQuery,
  useLazyGetTenantDashboardQuery,
} = tenantApi;

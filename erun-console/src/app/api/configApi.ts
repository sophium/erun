import { buildPlatformConfigEndpoints } from 'erun-kit';

import { platformApi } from './platformApi';

// The shared endpoint definition from erun-kit/src/api/platformConfigEndpoints.ts.
export const configApi = platformApi.injectEndpoints({
  endpoints: (builder) => buildPlatformConfigEndpoints(builder),
});

export const { useGetConfigQuery, useLazyGetConfigQuery } = configApi;

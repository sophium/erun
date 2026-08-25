import { buildPlatformConfigEndpoints } from 'erun-kit';

import { platformApi } from './platformApi';

// The one shared endpoint definition from erun-kit (erun#1283) — see
// erun-kit/src/api/platformConfigEndpoints.ts and its "same model over both
// transports" proof in erun-kit's and erun-ui/frontend's own test suites.
export const configApi = platformApi.injectEndpoints({
  endpoints: (builder) => buildPlatformConfigEndpoints(builder),
});

export const { useGetConfigQuery, useLazyGetConfigQuery } = configApi;

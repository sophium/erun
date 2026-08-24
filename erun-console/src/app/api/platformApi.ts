import { createApi } from '@reduxjs/toolkit/query/react';

import { httpBaseQuery } from './httpBaseQuery';

// NoValue stands in for `void` in generic positions, which
// @typescript-eslint/no-invalid-void-type forbids; the ReturnType wrapper
// captures the one place the rule permits `void` so the alias itself is
// legal. Mirrors erun-ui/frontend/src/app/api/wailsBaseQuery.ts's NoValue.
type _VoidReturning = () => void;
export type NoValue = ReturnType<_VoidReturning>;

// Single createApi instance keeps a unified cache, tag namespace, and reducer
// slot, mirroring the desktop's wailsApi (erun-ui/frontend/src/app/api/
// wailsApi.ts). Per-domain modules add endpoints via injectEndpoints.
export const platformApi = createApi({
  reducerPath: 'platformApi',
  baseQuery: httpBaseQuery,
  tagTypes: ['Config', 'Environment', 'Context', 'IdentityUsers', 'OrgSettings'],
  endpoints: () => ({}),
});

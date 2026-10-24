import { createApi } from '@reduxjs/toolkit/query/react';

import { httpBaseQuery } from './httpBaseQuery';

// NoValue stands in for `void` in generic positions, which @typescript-eslint/no-invalid-void-type forbids.
type _VoidReturning = () => void;
export type NoValue = ReturnType<_VoidReturning>;

// One createApi instance shares cache, tags and reducer slot; domains use injectEndpoints.
export const platformApi = createApi({
  reducerPath: 'platformApi',
  baseQuery: httpBaseQuery,
  tagTypes: [
    'Config',
    'Environment',
    'Environments',
    'Context',
    'IdentityUsers',
    'PlatformUsers',
    'OrgSettings',
    'SmtpSettings',
    'Invites',
    'Roles',
    'UserRoles',
    'Tenants',
    'Quota',
    'PendingRequests',
    'TenantIssuers',
    'Jobs',
    'Pipeline',
  ],
  endpoints: () => ({}),
});

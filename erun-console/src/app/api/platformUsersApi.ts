// RTK Query endpoints for /v1/users: enrol an erun user for an existing identity,
// the only enrollment path that can target another tenant.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

export interface PlatformUser {
  userId: string;
  tenantId: string;
  username: string;
  issuer?: string;
  subject?: string;
}

function parsePlatformUser(raw: Record<string, unknown>): PlatformUser {
  return {
    userId: asString(raw.userId),
    tenantId: asString(raw.tenantId),
    username: asString(raw.username),
    issuer: asOptionalString(raw.issuer),
    subject: asOptionalString(raw.subject),
  };
}

function parsePlatformUserList(value: unknown): PlatformUser[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parsePlatformUser);
}

// tenantId is honored only for an operations-tenant caller; the backend is the real boundary.
export interface EnrollPlatformUserInput {
  username: string;
  issuer?: string;
  subject?: string;
  tenantId?: string;
}

// alreadyEnrolled marks the no-op 200; a username collision is a USERNAME_TAKEN error instead.
export interface EnrollPlatformUserResult {
  user: PlatformUser;
  alreadyEnrolled: boolean;
}

function parseEnrollPlatformUserResult(raw: unknown): EnrollPlatformUserResult {
  if (!isRecord(raw)) {
    throw new Error('enroll user response was not in the expected shape');
  }
  return { user: parsePlatformUser(raw), alreadyEnrolled: raw.alreadyEnrolled === true };
}

export const platformUsersApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    listPlatformUsers: builder.query<PlatformUser[], { token: string; tenantId?: string }>({
      query: ({ token, tenantId }) => ({
        url:
          tenantId !== undefined && tenantId.length > 0
            ? `/v1/users?tenantId=${encodeURIComponent(tenantId)}`
            : '/v1/users',
        token,
        label: 'list platform users',
      }),
      transformResponse: parsePlatformUserList,
      providesTags: ['PlatformUsers'],
    }),

    // Also invalidates 'Tenants': it changes the user count the inert-tenant flag reads.
    enrollPlatformUser: builder.mutation<
      EnrollPlatformUserResult,
      { token: string; input: EnrollPlatformUserInput }
    >({
      query: ({ token, input }) => ({
        url: '/v1/users',
        method: 'POST',
        body: input,
        token,
        label: 'enroll user',
      }),
      transformResponse: parseEnrollPlatformUserResult,
      invalidatesTags: ['PlatformUsers', 'Tenants'],
    }),
  }),
});

export const { useListPlatformUsersQuery, useEnrollPlatformUserMutation } = platformUsersApi;

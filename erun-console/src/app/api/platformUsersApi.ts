// RTK Query endpoints for /v1/users: the raw erun-side enrollment surface
// (erun-backend-api/internal/routes/users.go) that writes a user row
// directly, optionally linking an external (issuer, subject) identity —
// the same capability `erun platform user enroll` exposes on the CLI.
// Distinct from identityApi's /v1/identity/users, which also creates the IdP
// account itself; this one assumes the identity already exists (or will be
// linked later) and is the only enrollment path that can target a tenant
// other than the caller's own.
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

// EnrollPlatformUserInput mirrors POST /v1/users' body. tenantId targets a
// tenant other than the caller's own and is honored only for an
// operations-tenant caller (erun-backend-api's resolveTargetTenant); the
// backend refuses it outright for anyone else, so the console's own
// tenant-selector gating is presentation only, never the real boundary.
export interface EnrollPlatformUserInput {
  username: string;
  issuer?: string;
  subject?: string;
  tenantId?: string;
}

// alreadyEnrolled distinguishes the no-op "this identity is already enrolled
// in the target tenant" success (200) from a genuine username collision,
// which the backend reports as a USERNAME_TAKEN error instead (see
// httpBaseQuery's parsedError / queryError's describeQueryError for how that
// surfaces to a caller of enrollPlatformUser).
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

    // enrollPlatformUser invalidates 'Tenants' as well as 'PlatformUsers':
    // it is the one write that can change a tenant's user count, which the
    // Tenants view's inert-tenant flag (erun#1744) reads from listTenants.
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

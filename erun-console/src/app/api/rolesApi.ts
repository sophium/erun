// RTK Query endpoints for /v1/roles and /v1/users/{user_id}/roles: the
// tenant's role-assignment surface. List a tenant's roles, and list/grant/
// revoke a user's role assignments.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { type NoValue, platformApi } from './platformApi';

// A permission is either an exact method/path pair or a regex method/path
// pattern pair — mirroring role_permissions' own exact-or-pattern contract.
export interface RolePermission {
  rolePermissionId: string;
  roleId: string;
  apiMethod?: string;
  apiPath?: string;
  apiMethodPattern?: string;
  apiPathPattern?: string;
}

export interface Role {
  roleId: string;
  tenantId: string;
  name: string;
  permissions: RolePermission[];
  createdAt: string;
}

function parseRolePermission(raw: Record<string, unknown>): RolePermission {
  return {
    rolePermissionId: asString(raw.rolePermissionId),
    roleId: asString(raw.roleId),
    apiMethod: asOptionalString(raw.apiMethod),
    apiPath: asOptionalString(raw.apiPath),
    apiMethodPattern: asOptionalString(raw.apiMethodPattern),
    apiPathPattern: asOptionalString(raw.apiPathPattern),
  };
}

function parseRole(raw: Record<string, unknown>): Role {
  return {
    roleId: asString(raw.roleId),
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    permissions: Array.isArray(raw.permissions)
      ? raw.permissions.filter(isRecord).map(parseRolePermission)
      : [],
    createdAt: asString(raw.createdAt),
  };
}

function parseRoleList(value: unknown): Role[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseRole);
}

export const rolesApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // listRoles returns every role the caller's tenant has defined —
    // ReadAll/WriteAll plus whatever custom roles it created.
    listRoles: builder.query<Role[], string>({
      query: (token) => ({ url: '/v1/roles', token, label: 'list roles' }),
      transformResponse: parseRoleList,
      providesTags: ['Roles'],
    }),

    // listUserRoles returns exactly the roles held by one user — what a
    // "Manage roles" surface needs to render current grants and the
    // remaining grantable set.
    listUserRoles: builder.query<Role[], { token: string; userId: string }>({
      query: ({ token, userId }) => ({
        url: `/v1/users/${encodeURIComponent(userId)}/roles`,
        token,
        label: 'list user roles',
      }),
      transformResponse: parseRoleList,
      providesTags: (_result, _error, { userId }) => [{ type: 'UserRoles', id: userId }],
    }),

    grantUserRole: builder.mutation<NoValue, { token: string; userId: string; roleId: string }>({
      query: ({ token, userId, roleId }) => ({
        url: `/v1/users/${encodeURIComponent(userId)}/roles`,
        method: 'POST',
        body: { roleId },
        token,
        label: 'grant role',
      }),
      invalidatesTags: (_result, _error, { userId }) => [{ type: 'UserRoles', id: userId }],
    }),

    // revokeUserRole can be refused (409) when it would leave the tenant with
    // no user able to grant roles — the lockout guard the backend enforces.
    // The caller surfaces that refusal message; nothing here needs to know
    // its shape.
    revokeUserRole: builder.mutation<NoValue, { token: string; userId: string; roleId: string }>({
      query: ({ token, userId, roleId }) => ({
        url: `/v1/users/${encodeURIComponent(userId)}/roles/${encodeURIComponent(roleId)}`,
        method: 'DELETE',
        token,
        label: 'revoke role',
      }),
      invalidatesTags: (_result, _error, { userId }) => [{ type: 'UserRoles', id: userId }],
    }),
  }),
});

export const {
  useListRolesQuery,
  useListUserRolesQuery,
  useGrantUserRoleMutation,
  useRevokeUserRoleMutation,
} = rolesApi;

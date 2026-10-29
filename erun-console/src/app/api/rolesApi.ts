// RTK Query endpoints for tenant roles and user role assignments.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { type NoValue, platformApi } from './platformApi';

// Exact method/path or regex pattern pair, mirroring role_permissions.
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
    listRoles: builder.query<Role[], string>({
      query: (token) => ({ url: '/v1/roles', token, label: 'list roles' }),
      transformResponse: parseRoleList,
      providesTags: ['Roles'],
    }),

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

    // Refused with 409 when it would leave no user able to grant roles.
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

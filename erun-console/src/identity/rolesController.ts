import * as React from 'react';

import {
  type Role,
  useGrantUserRoleMutation,
  useListRolesQuery,
  useListUserRolesQuery,
  useRevokeUserRoleMutation,
} from '../app/api/rolesApi';
import { queryErrorMessage } from '../app/queryError';

function useActiveRef(): React.RefObject<boolean> {
  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);
  return activeRef;
}

export type UserRolesState =
  | { status: 'loading' }
  | { status: 'ready'; roles: Role[] }
  | { status: 'error'; message: string };

export interface UserRolesController {
  // tenantRoles is every role the tenant has defined — the grantable set a
  // "Manage roles" surface picks from, independent of whether the read
  // succeeded for this particular user.
  tenantRoles: Role[];
  userRolesState: UserRolesState;
  actionError: string | undefined;
  grant: (roleId: string) => Promise<void>;
  revoke: (roleId: string) => Promise<void>;
}

// useUserRolesController adapts rolesApi's tenant-role list and one user's
// role assignments behind a single controller: the tenant's full role list
// (to populate a grant picker) plus that user's currently held roles, and the
// grant/revoke mutations. Granting or revoking invalidates the user's own
// roles tag, so the dialog reflects the new grant set without a manual
// refresh — the same pattern useUsersController already uses for
// enroll/deactivate.
export function useUserRolesController(token: string, userId: string): UserRolesController {
  const tenantRolesQuery = useListRolesQuery(token);
  const userRolesQuery = useListUserRolesQuery({ token, userId });
  const [grantUserRole] = useGrantUserRoleMutation();
  const [revokeUserRole] = useRevokeUserRoleMutation();
  const [actionError, setActionError] = React.useState<string | undefined>(undefined);
  const activeRef = useActiveRef();

  const userRolesState: UserRolesState =
    userRolesQuery.error !== undefined
      ? { status: 'error', message: queryErrorMessage(userRolesQuery.error) }
      : userRolesQuery.data !== undefined
        ? { status: 'ready', roles: userRolesQuery.data }
        : { status: 'loading' };

  const grant = React.useCallback(
    (roleId: string): Promise<void> => {
      setActionError(undefined);
      return grantUserRole({ token, userId, roleId })
        .unwrap()
        .then(() => undefined)
        .catch((error: unknown) => {
          if (activeRef.current) {
            setActionError(queryErrorMessage(error));
          }
        });
    },
    [token, userId, activeRef, grantUserRole],
  );

  const revoke = React.useCallback(
    (roleId: string): Promise<void> => {
      setActionError(undefined);
      return revokeUserRole({ token, userId, roleId })
        .unwrap()
        .then(() => undefined)
        .catch((error: unknown) => {
          if (activeRef.current) {
            setActionError(queryErrorMessage(error));
          }
        });
    },
    [token, userId, activeRef, revokeUserRole],
  );

  return {
    tenantRoles: tenantRolesQuery.data ?? [],
    userRolesState,
    actionError,
    grant,
    revoke,
  };
}

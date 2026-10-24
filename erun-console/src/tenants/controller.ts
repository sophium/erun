import * as React from 'react';

import type { CreateTenantInput, PlatformTenant } from '../app/api/tenantsApi';
import { useCreateTenantMutation, useListTenantsQuery } from '../app/api/tenantsApi';
import { queryErrorMessage } from '../app/queryError';

export type TenantsState =
  | { status: 'loading' }
  | { status: 'ready'; tenants: PlatformTenant[] }
  | { status: 'error'; message: string };

// TenantFieldError is undefined when a failure is not about one field.
export type TenantFieldError = 'name' | 'issuer' | 'type';

export type CreateTenantState =
  | { status: 'idle' }
  | { status: 'creating' }
  | { status: 'created'; tenant: PlatformTenant }
  | { status: 'error'; message: string; field?: TenantFieldError };

export interface TenantsController {
  tenantsState: TenantsState;
  createState: CreateTenantState;
  create: (input: CreateTenantInput) => void;
  dismissCreated: () => void;
}

// The API has no structured field errors, so this matches the three distinct
// substrings POST /v1/tenants messages contain.
function classifyTenantFieldError(message: string): TenantFieldError | undefined {
  if (message.includes('issuer')) {
    return 'issuer';
  }
  if (message.includes('name')) {
    return 'name';
  }
  if (message.includes('type')) {
    return 'type';
  }
  return undefined;
}

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

export function useTenantsController(token: string): TenantsController {
  const listQuery = useListTenantsQuery(token);
  const [createState, setCreateState] = React.useState<CreateTenantState>({ status: 'idle' });
  const [createTenant] = useCreateTenantMutation();
  const activeRef = useActiveRef();

  const tenantsState: TenantsState =
    listQuery.error !== undefined
      ? { status: 'error', message: queryErrorMessage(listQuery.error) }
      : listQuery.data !== undefined
        ? { status: 'ready', tenants: listQuery.data }
        : { status: 'loading' };

  const create = React.useCallback(
    (input: CreateTenantInput) => {
      setCreateState({ status: 'creating' });
      createTenant({ token, input })
        .unwrap()
        .then((tenant) => {
          if (activeRef.current) {
            setCreateState({ status: 'created', tenant });
          }
        })
        .catch((error: unknown) => {
          if (!activeRef.current) {
            return;
          }
          const message = queryErrorMessage(error);
          setCreateState({ status: 'error', message, field: classifyTenantFieldError(message) });
        });
    },
    [token, activeRef, createTenant],
  );

  const dismissCreated = React.useCallback(() => {
    setCreateState({ status: 'idle' });
  }, []);

  return { tenantsState, createState, create, dismissCreated };
}

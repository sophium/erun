import * as React from 'react';

import type { CreateTenantInput, PlatformTenant } from '../app/api/tenantsApi';
import { useCreateTenantMutation, useListTenantsQuery } from '../app/api/tenantsApi';
import { queryErrorMessage } from '../app/queryError';

export type TenantsState =
  | { status: 'loading' }
  | { status: 'ready'; tenants: PlatformTenant[] }
  | { status: 'error'; message: string };

// TenantFieldError names the one field a create-tenant validation failure is
// about, when it can be told from the backend's own message (see
// classifyTenantFieldError below). `undefined` means the failure isn't about
// one field — e.g. a malformed body or the operations-only refusal — so the
// panel renders it as a general banner instead of pinning it to a control.
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

// classifyTenantFieldError reads which field a POST /v1/tenants 400/409
// message is about, from the message text itself — the API has no
// structured field-level error shape today (see
// erun-backend-api/internal/routes/tenants.go's parseCreateTenantParams and
// duplicateIssuerMessage), only these three distinct, non-overlapping
// substrings across every message that route can produce.
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

// useTenantsController lists tenants and registers a new one. Creating
// invalidates the list query's tag, the same pattern
// useUsersController/useInvitesController use for their own writes.
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

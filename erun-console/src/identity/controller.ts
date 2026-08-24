import * as React from 'react';

import {
  type EnrollIdentityUserInput,
  type EnrollIdentityUserResult,
  type IdentityUser,
  type OrgSettings,
  type UpdateOrgSettingsInput,
  useCreateIdentityUserMutation,
  useGetOrgSettingsQuery,
  useListIdentityUsersQuery,
  useSetIdentityUserActiveMutation,
  useUpdateOrgSettingsMutation,
} from '../app/api/identityApi';
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

export type UsersState =
  | { status: 'loading' }
  | { status: 'ready'; users: IdentityUser[] }
  | { status: 'error'; message: string };

export type EnrollState =
  | { status: 'idle' }
  | { status: 'enrolling' }
  | { status: 'enrolled'; result: EnrollIdentityUserResult }
  | { status: 'error'; message: string };

export interface UsersController {
  usersState: UsersState;
  enrollState: EnrollState;
  refresh: () => void;
  enroll: (input: EnrollIdentityUserInput) => void;
  setActive: (externalId: string, active: boolean) => void;
}

// useUsersController lists, enrolls, deactivates and reactivates identities.
// Enrolling or changing activation invalidates the list query's tag, so the
// operator sees the effect without a manual reload.
export function useUsersController(token: string): UsersController {
  const listQuery = useListIdentityUsersQuery(token);
  const { refetch } = listQuery;
  const [enrollState, setEnrollState] = React.useState<EnrollState>({ status: 'idle' });
  // setActive has no state slot of its own in this controller's public shape;
  // a failed toggle surfaces through usersState the same way the list read's
  // own failure does, mirroring the pre-RTK-Query controller's behaviour.
  const [actionError, setActionError] = React.useState<string | undefined>(undefined);
  const [createIdentityUser] = useCreateIdentityUserMutation();
  const [setIdentityUserActive] = useSetIdentityUserActiveMutation();
  const activeRef = useActiveRef();

  const usersState: UsersState =
    actionError !== undefined
      ? { status: 'error', message: actionError }
      : listQuery.error !== undefined
        ? { status: 'error', message: queryErrorMessage(listQuery.error) }
        : listQuery.data !== undefined
          ? { status: 'ready', users: listQuery.data }
          : { status: 'loading' };

  const refresh = React.useCallback(() => {
    void refetch();
  }, [refetch]);

  const enroll = React.useCallback(
    (input: EnrollIdentityUserInput) => {
      setEnrollState({ status: 'enrolling' });
      setActionError(undefined);
      createIdentityUser({ token, input })
        .unwrap()
        .then((result) => {
          if (!activeRef.current) {
            return;
          }
          setEnrollState({ status: 'enrolled', result });
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnrollState({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, activeRef, createIdentityUser],
  );

  const setActive = React.useCallback(
    (externalId: string, active: boolean) => {
      setActionError(undefined);
      setIdentityUserActive({ token, externalId, active })
        .unwrap()
        .catch((error: unknown) => {
          if (activeRef.current) {
            setActionError(queryErrorMessage(error));
          }
        });
    },
    [token, activeRef, setIdentityUserActive],
  );

  return { usersState, enrollState, refresh, enroll, setActive };
}

export type OrgSettingsState =
  | { status: 'loading' }
  | { status: 'ready'; settings: OrgSettings }
  | { status: 'saving'; settings: OrgSettings }
  | { status: 'error'; message: string };

export interface OrgSettingsController {
  state: OrgSettingsState;
  save: (input: UpdateOrgSettingsInput) => void;
}

// useOrgSettingsController reads the org's login/password policy once and
// applies partial updates. `updateResult`'s own data takes precedence over
// the read query's while a save is settling, so the form does not flicker
// back to the pre-save value while the invalidated read query refetches.
export function useOrgSettingsController(token: string): OrgSettingsController {
  const query = useGetOrgSettingsQuery(token);
  const [updateOrgSettings, updateResult] = useUpdateOrgSettingsMutation();

  const save = React.useCallback(
    (input: UpdateOrgSettingsInput) => {
      void updateOrgSettings({ token, input });
    },
    [token, updateOrgSettings],
  );

  const state: OrgSettingsState = (() => {
    if (updateResult.isError) {
      return { status: 'error', message: queryErrorMessage(updateResult.error) };
    }
    if (query.error !== undefined) {
      return { status: 'error', message: queryErrorMessage(query.error) };
    }
    const settings = updateResult.data ?? query.data;
    if (settings === undefined) {
      return { status: 'loading' };
    }
    return updateResult.isLoading ? { status: 'saving', settings } : { status: 'ready', settings };
  })();

  return { state, save };
}

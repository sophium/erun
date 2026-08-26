import * as React from 'react';

import {
  type EnrollIdentityUserInput,
  type EnrollIdentityUserResult,
  type IdentityUser,
  type OrgSettings,
  type SmtpStatus,
  type UpdateOrgSettingsInput,
  type UpdateSmtpSettingsInput,
  useCreateIdentityUserMutation,
  useGetOrgSettingsQuery,
  useGetSmtpSettingsQuery,
  useListIdentityUsersQuery,
  useSetIdentityUserActiveMutation,
  useUpdateOrgSettingsMutation,
  useUpdateSmtpSettingsMutation,
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
  dismissTemporaryPassword: () => void;
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

  // dismissTemporaryPassword strips the one-time credential out of the held
  // result once the operator has closed the dialog showing it, so it stops
  // sitting in this controller's state — the enrolled/error messaging above
  // it stays, only the secret itself is dropped.
  const dismissTemporaryPassword = React.useCallback(() => {
    setEnrollState((prev) =>
      prev.status === 'enrolled' && prev.result.temporaryPassword !== undefined
        ? { status: 'enrolled', result: { ...prev.result, temporaryPassword: undefined } }
        : prev,
    );
  }, []);

  return { usersState, enrollState, refresh, enroll, setActive, dismissTemporaryPassword };
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

// SmtpSettingsState keeps a failed save's settings around (status 'error'
// still carries the last-known settings) rather than replacing the form with
// a bare error message: the operator's inputs stay editable and resubmitting
// is the recovery path, instead of a dead end that only a page reload fixes.
export type SmtpSettingsState =
  | { status: 'loading' }
  | { status: 'load-error'; message: string }
  | { status: 'ready'; settings: SmtpStatus }
  | { status: 'saving'; settings: SmtpStatus }
  | { status: 'error'; settings: SmtpStatus; message: string };

export interface SmtpSettingsController {
  state: SmtpSettingsState;
  save: (input: UpdateSmtpSettingsInput) => void;
}

// useSmtpSettingsController reads the platform's outbound-mail configuration
// and applies updates, mirroring useOrgSettingsController's precedence rule
// (the update result's own data wins over the read query's while a save is
// settling).
export function useSmtpSettingsController(token: string): SmtpSettingsController {
  const query = useGetSmtpSettingsQuery(token);
  const [updateSmtpSettings, updateResult] = useUpdateSmtpSettingsMutation();

  const save = React.useCallback(
    (input: UpdateSmtpSettingsInput) => {
      void updateSmtpSettings({ token, input });
    },
    [token, updateSmtpSettings],
  );

  const state: SmtpSettingsState = (() => {
    const settings = updateResult.data ?? query.data;
    if (settings === undefined) {
      return query.error !== undefined
        ? { status: 'load-error', message: queryErrorMessage(query.error) }
        : { status: 'loading' };
    }
    if (updateResult.isError) {
      return { status: 'error', settings, message: queryErrorMessage(updateResult.error) };
    }
    return updateResult.isLoading ? { status: 'saving', settings } : { status: 'ready', settings };
  })();

  return { state, save };
}

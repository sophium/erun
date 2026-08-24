import * as React from 'react';

import type {
  EnrollIdentityUserInput,
  EnrollIdentityUserResult,
  IdentityUser,
  OrgSettings,
  UpdateOrgSettingsInput,
} from './client';
import {
  createIdentityUser,
  deactivateIdentityUser,
  getOrgSettings,
  listIdentityUsers,
  reactivateIdentityUser,
  updateOrgSettings,
} from './client';

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
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
// Enrolling or changing activation refreshes the list on success, so the
// operator sees the effect without a manual reload.
export function useUsersController(token: string): UsersController {
  const [usersState, setUsersState] = React.useState<UsersState>({ status: 'loading' });
  const [enrollState, setEnrollState] = React.useState<EnrollState>({ status: 'idle' });
  const activeRef = useActiveRef();

  const refresh = React.useCallback(() => {
    setUsersState({ status: 'loading' });
    listIdentityUsers(token)
      .then((users) => {
        if (activeRef.current) {
          setUsersState({ status: 'ready', users });
        }
      })
      .catch((error: unknown) => {
        if (activeRef.current) {
          setUsersState({ status: 'error', message: errorMessage(error) });
        }
      });
  }, [token, activeRef]);

  React.useEffect(refresh, [refresh]);

  const enroll = React.useCallback(
    (input: EnrollIdentityUserInput) => {
      setEnrollState({ status: 'enrolling' });
      createIdentityUser(token, input)
        .then((result) => {
          if (!activeRef.current) {
            return;
          }
          setEnrollState({ status: 'enrolled', result });
          refresh();
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnrollState({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, activeRef, refresh],
  );

  const setActive = React.useCallback(
    (externalId: string, active: boolean) => {
      const request = active
        ? reactivateIdentityUser(token, externalId)
        : deactivateIdentityUser(token, externalId);
      request
        .then(() => {
          if (activeRef.current) {
            refresh();
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setUsersState({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, activeRef, refresh],
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
// applies partial updates, keeping the last-known settings visible under
// `saving` so the form does not blank out while a save is in flight.
export function useOrgSettingsController(token: string): OrgSettingsController {
  const [state, setState] = React.useState<OrgSettingsState>({ status: 'loading' });
  const activeRef = useActiveRef();

  React.useEffect(() => {
    getOrgSettings(token)
      .then((settings) => {
        if (activeRef.current) {
          setState({ status: 'ready', settings });
        }
      })
      .catch((error: unknown) => {
        if (activeRef.current) {
          setState({ status: 'error', message: errorMessage(error) });
        }
      });
  }, [token, activeRef]);

  const save = React.useCallback(
    (input: UpdateOrgSettingsInput) => {
      setState((current) =>
        current.status === 'ready' || current.status === 'saving'
          ? { status: 'saving', settings: current.settings }
          : current,
      );
      updateOrgSettings(token, input)
        .then((settings) => {
          if (activeRef.current) {
            setState({ status: 'ready', settings });
          }
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, activeRef],
  );

  return { state, save };
}

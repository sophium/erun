import * as React from 'react';

import type { EnrollPlatformUserInput, PlatformUser } from '../app/api/platformUsersApi';
import { useEnrollPlatformUserMutation } from '../app/api/platformUsersApi';
import { describeQueryError } from '../app/queryError';

// USERNAME_TAKEN is 'conflict', apart from a no-op re-enrollment and generic errors.
export type PlatformEnrollState =
  | { status: 'idle' }
  | { status: 'enrolling' }
  | { status: 'enrolled'; user: PlatformUser; alreadyEnrolled: boolean }
  | { status: 'conflict'; message: string }
  | { status: 'error'; message: string };

export interface PlatformEnrollController {
  state: PlatformEnrollState;
  enroll: (input: EnrollPlatformUserInput) => void;
  reset: () => void;
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

export function usePlatformEnrollController(token: string): PlatformEnrollController {
  const [enrollPlatformUser] = useEnrollPlatformUserMutation();
  const [state, setState] = React.useState<PlatformEnrollState>({ status: 'idle' });
  const activeRef = useActiveRef();

  const enroll = React.useCallback(
    (input: EnrollPlatformUserInput) => {
      setState({ status: 'enrolling' });
      enrollPlatformUser({ token, input })
        .unwrap()
        .then((result) => {
          if (!activeRef.current) {
            return;
          }
          setState({
            status: 'enrolled',
            user: result.user,
            alreadyEnrolled: result.alreadyEnrolled,
          });
        })
        .catch((error: unknown) => {
          if (!activeRef.current) {
            return;
          }
          const described = describeQueryError(error);
          setState(
            described.code === 'USERNAME_TAKEN'
              ? { status: 'conflict', message: described.message }
              : { status: 'error', message: described.message },
          );
        });
    },
    [token, activeRef, enrollPlatformUser],
  );

  const reset = React.useCallback(() => {
    setState({ status: 'idle' });
  }, []);

  return { state, enroll, reset };
}

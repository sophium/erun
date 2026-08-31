import * as React from 'react';

import type { EnrollPlatformUserInput, PlatformUser } from '../app/api/platformUsersApi';
import { useEnrollPlatformUserMutation } from '../app/api/platformUsersApi';
import { describeQueryError } from '../app/queryError';

// PlatformEnrollState discriminates the three outcomes POST /v1/users can
// report: a no-op re-enrollment (alreadyEnrolled true on the 'enrolled'
// branch), a genuine username collision (its own USERNAME_TAKEN code, kept
// apart as 'conflict' rather than falling into the generic 'error' bucket),
// and every other failure. Collapsing the first two into one message is
// exactly the gap erun#1744 asks to close.
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

// usePlatformEnrollController wraps POST /v1/users' mutation and classifies
// its rejection by the backend's own machine code, rather than rendering
// every non-2xx response as the same generic failure text.
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

import * as React from 'react';

import { createEnvironment, type CreateEnvironmentInput } from '../config/client';
import type { Environment } from '../config/types';

// Thin controller for the env-registration panel: it owns the create-request
// state and calls the typed client (createEnvironment). No business logic lives
// here — the render layer (RegisterEnvPanel) shows whatever state this hook
// exposes. On success it invokes onRegistered so the parent can refresh the
// read model (the new env then appears in the config view + deploy panel).

export type RegisterEnvState =
  | { status: 'idle' }
  | { status: 'creating' }
  | { status: 'created'; environment: Environment }
  | { status: 'error'; message: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
}

export interface RegisterEnvController {
  state: RegisterEnvState;
  register: (input: CreateEnvironmentInput) => void;
}

export function useRegisterEnvController(token: string, onRegistered?: () => void): RegisterEnvController {
  const [state, setState] = React.useState<RegisterEnvState>({ status: 'idle' });

  // Guards the async resolution against running after the component unmounts.
  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const register = React.useCallback(
    (input: CreateEnvironmentInput) => {
      setState({ status: 'creating' });
      createEnvironment(token, input)
        .then((environment) => {
          if (!activeRef.current) {
            return;
          }
          setState({ status: 'created', environment });
          onRegistered?.();
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, onRegistered],
  );

  return { state, register };
}

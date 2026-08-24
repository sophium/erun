import type { Environment } from 'erun-kit';
import * as React from 'react';

import {
  type CreateEnvironmentInput,
  type DeployOutcome,
  environmentsApi,
  useCreateEnvironmentMutation,
  useDeployEnvironmentMutation,
} from '../app/api/environmentsApi';
import { useAppDispatch } from '../app/hooks';
import { queryErrorMessage } from '../app/queryError';

// Thin controllers over the RTK Query endpoints for the environments panel:
// one for registering a new environment, one (keyed by environmentId) for
// deploying an already-registered one and polling it to a terminal status.
// No business logic beyond sequencing those calls lives here — the render
// layer (EnvironmentsPanel) shows whatever state these hooks expose.

const POLL_INTERVAL_MS = 2000;

export type RegisterState =
  | { status: 'idle' }
  | { status: 'creating' }
  | { status: 'created'; environment: Environment }
  | { status: 'error'; message: string };

// State of one environment's deploy + poll flow. `conflict` (409, a deploy
// already in flight) and `unavailable` (501, no deploy executor configured) are
// their own terminal-ish states rather than folded into `error`, so the render
// layer can show them as the real, non-error states they are.
export type DeployState =
  | { status: 'starting' }
  | { status: 'deploying'; environment: Environment }
  | { status: 'running'; environment: Environment }
  | { status: 'failed'; environment: Environment }
  | { status: 'conflict' }
  | { status: 'unavailable' }
  | { status: 'error'; message: string };

function isTerminal(environment: Environment): boolean {
  return environment.status === 'running' || environment.status === 'failed';
}

function terminalState(environment: Environment): DeployState {
  return environment.status === 'failed'
    ? { status: 'failed', environment }
    : { status: 'running', environment };
}

export interface RegisterController {
  state: RegisterState;
  register: (input: CreateEnvironmentInput) => void;
}

// useRegisterEnvironmentController posts the create request and, on success,
// invokes onRegistered so the parent can refresh the read model — the new
// environment then appears in the deploy list below.
export function useRegisterEnvironmentController(
  token: string,
  onRegistered?: () => void,
): RegisterController {
  const [state, setState] = React.useState<RegisterState>({ status: 'idle' });
  const [createEnvironment] = useCreateEnvironmentMutation();

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
      createEnvironment({ token, input })
        .unwrap()
        .then((environment) => {
          if (!activeRef.current) {
            return;
          }
          setState({ status: 'created', environment });
          onRegistered?.();
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setState({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, onRegistered, createEnvironment],
  );

  return { state, register };
}

export interface DeployController {
  // Per-environmentId deploy state; an absent entry means the env has no
  // in-session deploy yet (the persisted status from the config read still shows).
  states: Record<string, DeployState>;
  deploy: (environmentId: string, version?: string) => void;
}

// useDeployController drives one environment's deploy + poll at a time per
// environmentId, so several environments can deploy independently in one
// session. onSettled fires once a deploy reaches running/failed, so the parent
// can refresh the read model and pick up the new status/deployedVersion there too.
export function useDeployController(token: string, onSettled?: () => void): DeployController {
  const [states, setStates] = React.useState<Record<string, DeployState>>({});
  const dispatch = useAppDispatch();
  const [deployEnvironment] = useDeployEnvironmentMutation();

  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const setEnvState = React.useCallback((environmentId: string, state: DeployState) => {
    setStates((prev) => ({ ...prev, [environmentId]: state }));
  }, []);

  const poll = React.useCallback(
    (environmentId: string) => {
      if (!activeRef.current) {
        return;
      }
      dispatch(
        environmentsApi.endpoints.getEnvironment.initiate(
          { token, environmentId },
          { forceRefetch: true },
        ),
      )
        .unwrap()
        .then((environment) => {
          if (!activeRef.current) {
            return;
          }
          if (isTerminal(environment)) {
            setEnvState(environmentId, terminalState(environment));
            onSettled?.();
            return;
          }
          setEnvState(environmentId, { status: 'deploying', environment });
          setTimeout(() => {
            poll(environmentId);
          }, POLL_INTERVAL_MS);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, dispatch, setEnvState, onSettled],
  );

  const deploy = React.useCallback(
    (environmentId: string, version?: string) => {
      setEnvState(environmentId, { status: 'starting' });
      deployEnvironment({ token, environmentId, version })
        .unwrap()
        .then((outcome: DeployOutcome) => {
          if (!activeRef.current) {
            return;
          }
          if (outcome.kind === 'conflict') {
            setEnvState(environmentId, { status: 'conflict' });
            return;
          }
          if (outcome.kind === 'unavailable') {
            setEnvState(environmentId, { status: 'unavailable' });
            return;
          }
          if (isTerminal(outcome.environment)) {
            setEnvState(environmentId, terminalState(outcome.environment));
            onSettled?.();
            return;
          }
          setEnvState(environmentId, { status: 'deploying', environment: outcome.environment });
          poll(environmentId);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, deployEnvironment, poll, setEnvState, onSettled],
  );

  return { states, deploy };
}

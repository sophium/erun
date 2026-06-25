import * as React from 'react';

import { deployEnvironment, getEnvironment } from '../config/client';
import type { Environment } from '../config/types';

// Thin controller for the deploy panel: it owns the per-environment
// request/poll state and calls the typed client (deployEnvironment /
// getEnvironment). It holds no business logic beyond sequencing those calls —
// the render layer (DeployPanel) shows whatever state this hook exposes. State
// is keyed by environmentId so several envs can deploy independently.

// How often the deploy flow polls GET /v1/environments/{id} while the env is
// still `deploying`.
const POLL_INTERVAL_MS = 2000;

// State of one environment's deploy + poll flow.
export type EnvDeployState =
  | { status: 'starting' }
  // Accepted (202) and polling getEnvironment; `environment` carries the latest poll.
  | { status: 'deploying'; environment: Environment }
  | { status: 'deployed'; environment: Environment }
  | { status: 'failed'; environment: Environment }
  | { status: 'error'; message: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
}

function isTerminal(environment: Environment): boolean {
  return environment.deployStatus === 'deployed' || environment.deployStatus === 'failed';
}

function terminalState(environment: Environment): EnvDeployState {
  return environment.deployStatus === 'failed'
    ? { status: 'failed', environment }
    : { status: 'deployed', environment };
}

export interface DeployController {
  // Per-environmentId deploy state; an absent entry means the env has no
  // in-session deploy yet (the persisted status from the config read still shows).
  states: Record<string, EnvDeployState>;
  deploy: (environmentId: string, version?: string) => void;
}

export function useDeployController(token: string): DeployController {
  const [states, setStates] = React.useState<Record<string, EnvDeployState>>({});

  // Guards the poll loop against running after the component unmounts.
  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const setEnvState = React.useCallback((environmentId: string, state: EnvDeployState) => {
    setStates((prev) => ({ ...prev, [environmentId]: state }));
  }, []);

  const poll = React.useCallback(
    (environmentId: string) => {
      if (!activeRef.current) {
        return;
      }
      getEnvironment(token, environmentId)
        .then((environment) => {
          if (!activeRef.current) {
            return;
          }
          if (isTerminal(environment)) {
            setEnvState(environmentId, terminalState(environment));
            return;
          }
          setEnvState(environmentId, { status: 'deploying', environment });
          setTimeout(() => {
            poll(environmentId);
          }, POLL_INTERVAL_MS);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, setEnvState],
  );

  const deploy = React.useCallback(
    (environmentId: string, version?: string) => {
      setEnvState(environmentId, { status: 'starting' });
      deployEnvironment(token, environmentId, version)
        .then((environment) => {
          if (!activeRef.current) {
            return;
          }
          if (isTerminal(environment)) {
            setEnvState(environmentId, terminalState(environment));
            return;
          }
          setEnvState(environmentId, { status: 'deploying', environment });
          poll(environmentId);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setEnvState(environmentId, { status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, poll, setEnvState],
  );

  return { states, deploy };
}

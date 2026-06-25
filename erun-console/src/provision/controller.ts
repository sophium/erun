import * as React from 'react';

import {
  createContext,
  type CreateContextInput,
  getContext,
  setCloudProviderAlias,
} from '../config/client';
import type { CloudContext } from '../config/types';

// Thin controller for the provisioning panel: it owns the request/poll state and
// calls the typed client (setCloudProviderAlias / createContext / getContext).
// It holds no business logic beyond sequencing those calls — the render layer
// (ProvisionPanel) shows whatever state this hook exposes.

// How often the create-context flow polls GET /v1/contexts/{id} while the
// context is still `provisioning`.
const POLL_INTERVAL_MS = 2000;

// State of the alias-registration request.
export type AliasState =
  | { status: 'idle' }
  | { status: 'saving' }
  | { status: 'saved' }
  | { status: 'error'; message: string };

// State of the create-context + poll flow.
export type ProvisionState =
  | { status: 'idle' }
  | { status: 'creating' }
  // Registered (202) and polling getContext; `context` carries the latest poll.
  | { status: 'polling'; context: CloudContext }
  | { status: 'running'; context: CloudContext }
  | { status: 'failed'; context: CloudContext }
  | { status: 'error'; message: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'unexpected error';
}

function isTerminal(context: CloudContext): boolean {
  return context.status === 'running' || context.status === 'failed';
}

function terminalState(context: CloudContext): ProvisionState {
  return context.status === 'failed'
    ? { status: 'failed', context }
    : { status: 'running', context };
}

export interface ProvisionController {
  alias: AliasState;
  provision: ProvisionState;
  saveAlias: (alias: string, provider: string, credentials: string) => void;
  provisionContext: (input: CreateContextInput) => void;
}

export function useProvisionController(token: string): ProvisionController {
  const [alias, setAlias] = React.useState<AliasState>({ status: 'idle' });
  const [provision, setProvision] = React.useState<ProvisionState>({ status: 'idle' });

  // Guards the poll loop against running after the component unmounts.
  const activeRef = React.useRef(true);
  React.useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const saveAlias = React.useCallback(
    (aliasName: string, provider: string, credentials: string) => {
      setAlias({ status: 'saving' });
      setCloudProviderAlias(token, aliasName, { provider, credentials })
        .then(() => {
          setAlias({ status: 'saved' });
        })
        .catch((error: unknown) => {
          setAlias({ status: 'error', message: errorMessage(error) });
        });
    },
    [token],
  );

  const poll = React.useCallback(
    (contextId: string) => {
      if (!activeRef.current) {
        return;
      }
      getContext(token, contextId)
        .then((context) => {
          if (!activeRef.current) {
            return;
          }
          if (isTerminal(context)) {
            setProvision(terminalState(context));
            return;
          }
          setProvision({ status: 'polling', context });
          setTimeout(() => {
            poll(contextId);
          }, POLL_INTERVAL_MS);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setProvision({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token],
  );

  const provisionContext = React.useCallback(
    (input: CreateContextInput) => {
      setProvision({ status: 'creating' });
      createContext(token, input)
        .then((context) => {
          if (!activeRef.current) {
            return;
          }
          if (isTerminal(context)) {
            setProvision(terminalState(context));
            return;
          }
          setProvision({ status: 'polling', context });
          poll(context.contextId);
        })
        .catch((error: unknown) => {
          if (activeRef.current) {
            setProvision({ status: 'error', message: errorMessage(error) });
          }
        });
    },
    [token, poll],
  );

  return { alias, provision, saveAlias, provisionContext };
}

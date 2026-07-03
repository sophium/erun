import * as React from 'react';

import {
  createContext,
  type CreateContextInput,
  getContext,
  setCloudProviderAlias,
} from '../config/client';
import type { CloudContext } from '../config/types';

const POLL_INTERVAL_MS = 2000;

export type AliasState =
  | { status: 'idle' }
  | { status: 'saving' }
  | { status: 'saved' }
  | { status: 'error'; message: string };

export type ProvisionState =
  | { status: 'idle' }
  | { status: 'creating' }
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

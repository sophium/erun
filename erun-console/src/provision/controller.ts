import type { CloudContext } from 'erun-kit';
import * as React from 'react';

import {
  contextsApi,
  type CreateContextInput,
  useCreateContextMutation,
  useSetCloudProviderAliasMutation,
} from '../app/api/contextsApi';
import { useAppDispatch } from '../app/hooks';
import { queryErrorMessage } from '../app/queryError';

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
  const dispatch = useAppDispatch();
  const [setCloudProviderAlias] = useSetCloudProviderAliasMutation();
  const [createContext] = useCreateContextMutation();

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
      setCloudProviderAlias({ token, alias: aliasName, input: { provider, credentials } })
        .unwrap()
        .then(() => {
          setAlias({ status: 'saved' });
        })
        .catch((error: unknown) => {
          setAlias({ status: 'error', message: queryErrorMessage(error) });
        });
    },
    [token, setCloudProviderAlias],
  );

  const poll = React.useCallback(
    (contextId: string) => {
      if (!activeRef.current) {
        return;
      }
      dispatch(
        contextsApi.endpoints.getContext.initiate({ token, contextId }, { forceRefetch: true }),
      )
        .unwrap()
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
            setProvision({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, dispatch],
  );

  const provisionContext = React.useCallback(
    (input: CreateContextInput) => {
      setProvision({ status: 'creating' });
      createContext({ token, input })
        .unwrap()
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
            setProvision({ status: 'error', message: queryErrorMessage(error) });
          }
        });
    },
    [token, createContext, poll],
  );

  return { alias, provision, saveAlias, provisionContext };
}

import * as React from 'react';

import { devBearerToken } from './auth/auth';
import { ConfigFetchError, fetchConfig } from './config/client';
import { ConfigView } from './config/ConfigView';
import type { TenantConfigView } from './config/types';
import { MCPAccessPanel } from './mcp/MCPAccessPanel';
import { ProvisionPanel } from './provision/ProvisionPanel';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView }
  | { status: 'signed-out' }
  | { status: 'error'; message: string };

// No real Sign in button yet: OIDC login is still a placeholder, so this
// prompt only explains why there is nothing to show without a verified token.
function SignInPrompt(): React.ReactElement {
  return (
    <div className="message" role="status">
      <p>Sign in to view your environments.</p>
    </div>
  );
}

function ErrorMessage({ message }: { message: string }): React.ReactElement {
  return (
    <div className="message" role="alert">
      <p>Could not load your environments.</p>
      <p className="error-detail">{message}</p>
    </div>
  );
}

function loadStateFromError(error: unknown): LoadState {
  if (error instanceof ConfigFetchError && error.status === 401) {
    return { status: 'signed-out' };
  }
  const message = error instanceof Error ? error.message : 'unexpected error';
  return { status: 'error', message };
}

export function App(): React.ReactElement {
  const [state, setState] = React.useState<LoadState>({ status: 'loading' });
  const token = React.useMemo(() => devBearerToken(), []);

  React.useEffect(() => {
    if (token === undefined) {
      setState({ status: 'signed-out' });
      return;
    }

    let active = true;
    fetchConfig(token)
      .then((config) => {
        if (active) {
          setState({ status: 'ready', config });
        }
      })
      .catch((error: unknown) => {
        if (active) {
          setState(loadStateFromError(error));
        }
      });

    return () => {
      active = false;
    };
  }, [token]);

  return (
    <main className="app">
      {state.status === 'loading' && (
        <div className="message" role="status">
          <p>Loading your environments…</p>
        </div>
      )}
      {state.status === 'signed-out' && <SignInPrompt />}
      {state.status === 'error' && <ErrorMessage message={state.message} />}
      {state.status === 'ready' && <ConfigView config={state.config} />}
      {token !== undefined && state.status === 'ready' && <ProvisionPanel token={token} />}
      {token !== undefined && state.status === 'ready' && (
        <MCPAccessPanel token={token} environments={state.config.environments} />
      )}
    </main>
  );
}

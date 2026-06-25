import * as React from 'react';

import { devBearerToken } from './auth/auth';
import { ConfigFetchError, fetchConfig } from './config/client';
import { ConfigView } from './config/ConfigView';
import type { TenantConfigView } from './config/types';
import { DeployPanel } from './deploy/DeployPanel';
import { RegisterEnvPanel } from './environments/RegisterEnvPanel';
import { ProvisionPanel } from './provision/ProvisionPanel';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView }
  | { status: 'signed-out' }
  | { status: 'error'; message: string };

// The sign-in prompt the API's 401 maps to. A real Sign in button lands with
// the OIDC flow (TODO(#606) in src/auth/auth.ts); until then it explains why
// there is nothing to show — the read view requires a verified token.
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
  // The dev token is read once; it gates both the config fetch and the
  // provisioning panel (which is only shown when a token is present). Replaced
  // by the OIDC-derived token once login() lands (TODO(#606) in src/auth/auth.ts).
  const token = React.useMemo(() => devBearerToken(), []);

  // Guards against setting state after unmount; also lets loadConfig be reused
  // as a post-registration refresh (so a newly-registered env appears in the
  // config view + deploy panel) without per-call cleanup handling.
  const mountedRef = React.useRef(true);
  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const loadConfig = React.useCallback(() => {
    if (token === undefined) {
      setState({ status: 'signed-out' });
      return;
    }
    fetchConfig(token)
      .then((config) => {
        if (mountedRef.current) {
          setState({ status: 'ready', config });
        }
      })
      .catch((error: unknown) => {
        if (mountedRef.current) {
          setState(loadStateFromError(error));
        }
      });
  }, [token]);

  React.useEffect(() => {
    loadConfig();
  }, [loadConfig]);

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
      {token !== undefined && state.status === 'ready' && (
        <>
          <ProvisionPanel token={token} />
          <RegisterEnvPanel
            token={token}
            contexts={state.config.contexts}
            onRegistered={loadConfig}
          />
          <DeployPanel token={token} environments={state.config.environments} />
        </>
      )}
    </main>
  );
}

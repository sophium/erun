import * as React from 'react';

import { beginLogin, type OidcConfig, oidcConfig, resolveToken } from './auth/auth';
import { ConfigFetchError, fetchConfig } from './config/client';
import { ConfigView } from './config/ConfigView';
import type { TenantConfigView } from './config/types';
import { DeployPanel } from './deploy/DeployPanel';
import { RegisterEnvPanel } from './environments/RegisterEnvPanel';
import { MCPAccessPanel } from './mcp/MCPAccessPanel';
import { ProvisionPanel } from './provision/ProvisionPanel';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView }
  | { status: 'signed-out' }
  | { status: 'error'; message: string };

// The signed-out view. With OIDC configured it offers a real Sign in button
// (Authorization Code + PKCE redirect to the issuer); without it (local dev /
// e2e), it explains that a token is required.
function SignInPrompt({ oidc }: { oidc: OidcConfig | undefined }): React.ReactElement {
  if (oidc === undefined) {
    return (
      <div className="message" role="status">
        <p>Sign in to view your environments.</p>
      </div>
    );
  }
  return (
    <div className="message" role="status">
      <p>Sign in to view your environments.</p>
      <button
        type="button"
        onClick={() => {
          void beginLogin(oidc);
        }}
      >
        Sign in
      </button>
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
  const oidc = React.useMemo(oidcConfig, []);
  const [state, setState] = React.useState<LoadState>({ status: 'loading' });
  // The bearer token: resolved once on mount (an OIDC callback exchange, a token
  // held this session, or the dev-token fallback). undefined until resolved, or
  // when the operator must sign in. It gates both the config fetch and the
  // action panels (only shown when a token is present).
  const [token, setToken] = React.useState<string | undefined>(undefined);

  // Guards against setting state after unmount; also lets loadConfig be reused as
  // a post-registration refresh (so a newly-registered env appears in the config
  // view + deploy panel) without per-call cleanup handling.
  const mountedRef = React.useRef(true);
  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Phase 1: resolve the bearer token (OIDC callback exchange / held token / dev
  // token). No token resolved → signed out.
  React.useEffect(() => {
    resolveToken(oidc)
      .then((resolved) => {
        if (!mountedRef.current) {
          return;
        }
        if (resolved === undefined) {
          setState({ status: 'signed-out' });
          return;
        }
        setToken(resolved);
      })
      .catch((error: unknown) => {
        if (mountedRef.current) {
          setState(loadStateFromError(error));
        }
      });
  }, [oidc]);

  // Phase 2: once a token is resolved, load the tenant config. Reused as the
  // post-registration refresh.
  const loadConfig = React.useCallback(() => {
    if (token === undefined) {
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
    if (token !== undefined) {
      loadConfig();
    }
  }, [token, loadConfig]);

  return (
    <main className="app">
      {state.status === 'loading' && (
        <div className="message" role="status">
          <p>Loading your environments…</p>
        </div>
      )}
      {state.status === 'signed-out' && <SignInPrompt oidc={oidc} />}
      {state.status === 'error' && <ErrorMessage message={state.message} />}
      {state.status === 'ready' && <ConfigView config={state.config} />}
      {token !== undefined && state.status === 'ready' && (
        <>
          <ProvisionPanel token={token} />
          <RegisterEnvPanel token={token} contexts={state.config.contexts} onRegistered={loadConfig} />
          <DeployPanel token={token} environments={state.config.environments} />
          <MCPAccessPanel token={token} environments={state.config.environments} />
        </>
      )}
    </main>
  );
}

import * as React from 'react';

import { beginLogin, type OidcConfig, oidcConfig, resolveToken, signOut } from './auth/auth';
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

// The signed-out view. With OIDC configured it offers a real Sign in button
// (Authorization Code + PKCE redirect to the issuer); without it (local dev), it
// only explains that a token is required.
function SignInPrompt({ oidc }: { oidc: OidcConfig | undefined }): React.ReactElement {
  return (
    <div className="message" role="status">
      <p>Sign in to view your environments.</p>
      {oidc !== undefined && (
        <button
          type="button"
          onClick={() => {
            void beginLogin(oidc);
          }}
        >
          Sign in
        </button>
      )}
    </div>
  );
}

// Signing out is the operator's only recovery from a session signed in as the
// wrong identity: it drops the held token and returns to the signed-out view.
function SignOutButton({ onSignedOut }: { onSignedOut: () => void }): React.ReactElement {
  return (
    <button
      type="button"
      onClick={() => {
        signOut();
        onSignedOut();
      }}
    >
      Sign out
    </button>
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

// The write surfaces, shown only once a token has produced a rendered config.
function ActionPanels({
  token,
  config,
}: {
  token: string;
  config: TenantConfigView;
}): React.ReactElement {
  return (
    <>
      <ProvisionPanel token={token} />
      <MCPAccessPanel token={token} environments={config.environments} />
    </>
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
  // The bearer token, resolved once on mount: an OIDC callback exchange, a token
  // held this session, or the dev-token fallback. It gates both the config fetch
  // and the action panels (only shown when a token is present).
  const [token, setToken] = React.useState<string | undefined>(undefined);

  const mountedRef = React.useRef(true);
  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Phase 1: resolve the bearer token. No token resolved → signed out.
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

  // Phase 2: once a token is resolved, load the tenant config.
  React.useEffect(() => {
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
      {state.status === 'ready' && token !== undefined && (
        <ActionPanels token={token} config={state.config} />
      )}
      {oidc !== undefined && token !== undefined && (
        <SignOutButton
          onSignedOut={() => {
            setToken(undefined);
            setState({ status: 'signed-out' });
          }}
        />
      )}
    </main>
  );
}

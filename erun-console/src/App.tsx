import * as React from 'react';

import { beginLogin, type OidcConfig, resolveOidcConfig, resolveToken, signOut } from './auth/auth';
import { ConfigFetchError, fetchConfig } from './config/client';
import { ConfigView } from './config/ConfigView';
import type { TenantConfigView } from './config/types';
import { EnvironmentsPanel } from './environments/EnvironmentsPanel';
import { MCPAccessPanel } from './mcp/MCPAccessPanel';
import { ProvisionPanel } from './provision/ProvisionPanel';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView }
  | { status: 'signed-out' }
  | { status: 'error'; message: string };

// The signed-out view. With OIDC configured it offers a real Sign in button
// (Authorization Code + PKCE redirect to the issuer); without it (local dev), it
// only explains that a token is required. `fallbackReason` surfaces why the
// local VITE_OIDC_* override is in play when platform discovery could not
// supply the config, so a misconfigured instance is visible rather than silent.
function SignInPrompt({
  oidc,
  fallbackReason,
}: {
  oidc: OidcConfig | undefined;
  fallbackReason: string | undefined;
}): React.ReactElement {
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
      {fallbackReason !== undefined && <p className="platform-fallback-note">{fallbackReason}</p>}
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
// onChanged lets a write surface trigger a config refetch so a newly
// registered environment or a settled deploy shows up in the read view above.
function ActionPanels({
  token,
  config,
  onChanged,
}: {
  token: string;
  config: TenantConfigView;
  onChanged: () => void;
}): React.ReactElement {
  return (
    <>
      <EnvironmentsPanel
        token={token}
        contexts={config.contexts}
        environments={config.environments}
        onChanged={onChanged}
      />
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
  // Phase 0: resolve the OIDC config from platform discovery (GET /v1/platform)
  // before anything else — it decides whether Sign in redirects anywhere.
  const [oidc, setOidc] = React.useState<OidcConfig | undefined>(undefined);
  const [oidcFallbackReason, setOidcFallbackReason] = React.useState<string | undefined>(undefined);
  const [oidcResolved, setOidcResolved] = React.useState(false);
  const [state, setState] = React.useState<LoadState>({ status: 'loading' });
  // The bearer token, resolved once oidc is known: an OIDC callback exchange, a
  // token held this session, or the dev-token fallback. It gates both the
  // config fetch and the action panels (only shown when a token is present).
  const [token, setToken] = React.useState<string | undefined>(undefined);

  const mountedRef = React.useRef(true);
  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  React.useEffect(() => {
    resolveOidcConfig()
      .then((resolution) => {
        if (!mountedRef.current) {
          return;
        }
        setOidc(resolution.config);
        setOidcFallbackReason(resolution.fallbackReason);
        setOidcResolved(true);
      })
      .catch(() => {
        // resolveOidcConfig never rejects (its own fetch is caught internally);
        // the handler exists only so a floating promise cannot slip through.
        if (mountedRef.current) {
          setOidcResolved(true);
        }
      });
  }, []);

  // Phase 1: resolve the bearer token. No token resolved → signed out.
  React.useEffect(() => {
    if (!oidcResolved) {
      return;
    }
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
  }, [oidcResolved, oidc]);

  // Phase 2: once a token is resolved, load the tenant config. loadConfig is
  // also the refresh a write surface (register/deploy) triggers on completion,
  // so a newly registered env or a settled deploy's status shows up here too.
  const loadConfig = React.useCallback((forToken: string) => {
    fetchConfig(forToken)
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
  }, []);

  React.useEffect(() => {
    if (token !== undefined) {
      loadConfig(token);
    }
  }, [token, loadConfig]);

  return (
    <main className="app">
      {state.status === 'loading' && (
        <div className="message" role="status">
          <p>Loading your environments…</p>
        </div>
      )}
      {state.status === 'signed-out' && (
        <SignInPrompt oidc={oidc} fallbackReason={oidcFallbackReason} />
      )}
      {state.status === 'error' && <ErrorMessage message={state.message} />}
      {state.status === 'ready' && <ConfigView config={state.config} />}
      {state.status === 'ready' && token !== undefined && (
        <ActionPanels
          token={token}
          config={state.config}
          onChanged={() => {
            loadConfig(token);
          }}
        />
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

import * as React from 'react';

import { beginLogin, type OidcConfig, resolveOidcConfig, resolveToken, signOut } from './auth/auth';
import { readTokenIdentity } from './auth/identity';
import { ConfigFetchError, fetchConfig } from './config/client';
import { ConfigView } from './config/ConfigView';
import type { TenantConfigView } from './config/types';
import { EnvironmentsPanel } from './environments/EnvironmentsPanel';
import { OrgSettingsPanel } from './identity/OrgSettingsPanel';
import { UsersPanel } from './identity/UsersPanel';
import { MCPAccessPanel } from './mcp/MCPAccessPanel';
import { ProvisionPanel } from './provision/ProvisionPanel';

// Identity administration (issue #1209) is restricted server-side to an
// OPERATIONS tenant; gating it here too keeps a COMPANY-tenant operator from
// ever seeing a form whose submit would just come back 403.
const OPERATIONS_TENANT_TYPE = 'OPERATIONS';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView }
  | { status: 'signed-out' }
  // Signed in with the identity provider, but the API does not recognise the
  // identity — it belongs to no tenant yet. Distinct from signed-out because
  // the recovery is completely different: signing in again just repeats the
  // same successful sign-in and lands here once more (#1167).
  | { status: 'not-enrolled'; token: string }
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

// The dead end a self-signed-up user hits: OIDC succeeded, so a token is held,
// but the API rejects the identity because it is enrolled in no tenant. Showing
// the signed-out prompt here was the bug — it offered Sign in, which succeeds
// and returns to this same screen forever, while a Sign out button sat beside
// it because a token did exist. This says what actually has to happen, and
// names the identity an operator needs in order to do it.
function NotEnrolledPrompt({ token }: { token: string }): React.ReactElement {
  const identity = readTokenIdentity(token);
  return (
    <div className="message" role="status">
      <p>You are signed in, but your account is not yet part of a tenant on this platform.</p>
      <p>
        An operator has to enrol you before you can see any environments. Signing in again will not
        change this.
      </p>
      {(identity.email !== undefined || identity.subject !== undefined) && (
        <p className="identity-detail">
          Give them this identity: {identity.email ?? identity.subject}
          {identity.email !== undefined && identity.subject !== undefined
            ? ` (subject ${identity.subject})`
            : ''}
          {identity.issuer !== undefined ? ` from ${identity.issuer}` : ''}
        </p>
      )}
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
      {config.tenant.type === OPERATIONS_TENANT_TYPE && (
        <>
          <UsersPanel token={token} />
          <OrgSettingsPanel token={token} />
        </>
      )}
    </>
  );
}

// A 401 means two completely different things depending on whether a token was
// held. With no token the caller is simply signed out. With a token, the
// identity provider authenticated them and the API still said no — so the
// identity is not enrolled, and telling them to sign in is a loop (#1167).
function loadStateFromError(error: unknown, token: string | undefined): LoadState {
  if (error instanceof ConfigFetchError && error.status === 401) {
    return token === undefined ? { status: 'signed-out' } : { status: 'not-enrolled', token };
  }
  const message = error instanceof Error ? error.message : 'unexpected error';
  return { status: 'error', message };
}

// LoadStateView renders whichever of the load states is current. Split out of
// App purely so App stays inside the module's max-lines-per-function budget;
// the exhaustive switch also means a new LoadState variant is a type error here
// rather than a silently blank screen.
function LoadStateView({
  state,
  oidc,
  fallbackReason,
}: {
  state: LoadState;
  oidc: OidcConfig | undefined;
  fallbackReason: string | undefined;
}): React.ReactElement {
  switch (state.status) {
    case 'loading':
      return (
        <div className="message" role="status">
          <p>Loading your environments…</p>
        </div>
      );
    case 'signed-out':
      return <SignInPrompt oidc={oidc} fallbackReason={fallbackReason} />;
    case 'not-enrolled':
      return <NotEnrolledPrompt token={state.token} />;
    case 'error':
      return <ErrorMessage message={state.message} />;
    case 'ready':
      return <ConfigView config={state.config} />;
  }
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
          // No token has been resolved yet on this path, so a 401 here is a
          // genuine signed-out.
          setState(loadStateFromError(error, undefined));
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
          setState(loadStateFromError(error, forToken));
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
      <LoadStateView state={state} oidc={oidc} fallbackReason={oidcFallbackReason} />
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

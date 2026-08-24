import { TooltipProvider } from 'erun-kit';
import * as React from 'react';

import { type OidcConfig, resolveOidcConfig, resolveToken, signOut } from './auth/auth';
import { ConfigFetchError, fetchConfig } from './config/client';
import { fetchPlatformConfig } from './config/platform';
import type { TenantConfigView } from './config/types';
import { AppShell } from './shell/AppShell';
import {
  ErrorScreen,
  LoadingScreen,
  NotEnrolledScreen,
  SignInScreen,
} from './shell/PreShellScreens';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView; token: string }
  | { status: 'signed-out' }
  // Signed in with the identity provider, but the API does not recognise the
  // identity — it belongs to no tenant yet. Distinct from signed-out because
  // the recovery is completely different: signing in again just repeats the
  // same successful sign-in and lands here once more (#1167).
  | { status: 'not-enrolled'; token: string }
  | { status: 'error'; message: string };

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

// useBrand resolves the platform's display name from discovery (GET
// /v1/platform) and mirrors it into the document title — the one thing that
// used to be a hardcoded literal (`erun console`) despite the value already
// being fetched and parsed for OIDC config. There is no logo/favicon field in
// the platform contract today, so the favicon stays the static build asset.
function useBrand(): string | undefined {
  const [brand, setBrand] = React.useState<string | undefined>(undefined);
  React.useEffect(() => {
    let cancelled = false;
    fetchPlatformConfig()
      .then((platform) => {
        if (!cancelled && platform !== undefined && platform.brand.length > 0) {
          setBrand(platform.brand);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);
  React.useEffect(() => {
    document.title = brand !== undefined ? `${brand} console` : 'ERun console';
  }, [brand]);
  return brand;
}

// useConsoleSession owns the sign-in → config-fetch lifecycle: resolve OIDC
// config, resolve a bearer token, then load the tenant config. loadConfig is
// also the refresh a write surface (register/deploy) triggers on completion,
// so a newly registered env or a settled deploy's status shows up here too.
function useConsoleSession(): {
  state: LoadState;
  oidc: OidcConfig | undefined;
  oidcFallbackReason: string | undefined;
  reload: () => void;
  signOutAndReset: () => void;
} {
  const [oidc, setOidc] = React.useState<OidcConfig | undefined>(undefined);
  const [oidcFallbackReason, setOidcFallbackReason] = React.useState<string | undefined>(undefined);
  const [oidcResolved, setOidcResolved] = React.useState(false);
  const [state, setState] = React.useState<LoadState>({ status: 'loading' });
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
        if (mountedRef.current) {
          setOidcResolved(true);
        }
      });
  }, []);

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
          setState(loadStateFromError(error, undefined));
        }
      });
  }, [oidcResolved, oidc]);

  const loadConfig = React.useCallback((forToken: string) => {
    fetchConfig(forToken)
      .then((config) => {
        if (mountedRef.current) {
          setState({ status: 'ready', config, token: forToken });
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

  return {
    state,
    oidc,
    oidcFallbackReason,
    reload: () => {
      if (token !== undefined) {
        loadConfig(token);
      }
    },
    signOutAndReset: () => {
      signOut();
      setToken(undefined);
      setState({ status: 'signed-out' });
    },
  };
}

function AppContent({
  brand,
  state,
  oidc,
  oidcFallbackReason,
  reload,
  signOutAndReset,
}: {
  brand: string | undefined;
  state: LoadState;
  oidc: OidcConfig | undefined;
  oidcFallbackReason: string | undefined;
  reload: () => void;
  signOutAndReset: () => void;
}): React.ReactElement {
  switch (state.status) {
    case 'loading':
      return <LoadingScreen brand={brand} />;
    case 'signed-out':
      return <SignInScreen brand={brand} oidc={oidc} fallbackReason={oidcFallbackReason} />;
    case 'not-enrolled':
      return <NotEnrolledScreen brand={brand} token={state.token} />;
    case 'error':
      return <ErrorScreen brand={brand} message={state.message} />;
    case 'ready':
      return (
        <AppShell
          brand={brand}
          token={state.token}
          config={state.config}
          onChanged={reload}
          onSignOut={signOutAndReset}
        />
      );
  }
}

export function App(): React.ReactElement {
  const brand = useBrand();
  const { state, oidc, oidcFallbackReason, reload, signOutAndReset } = useConsoleSession();

  // IconTooltip (used by the theme toggle and, once panels adopt it, elsewhere)
  // requires a TooltipProvider ancestor, same as the desktop's App.tsx.
  return (
    <TooltipProvider>
      <AppContent
        brand={brand}
        state={state}
        oidc={oidc}
        oidcFallbackReason={oidcFallbackReason}
        reload={reload}
        signOutAndReset={signOutAndReset}
      />
    </TooltipProvider>
  );
}

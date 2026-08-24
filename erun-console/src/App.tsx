import { type TenantConfigView, TooltipProvider } from 'erun-kit';
import * as React from 'react';

import { useGetConfigQuery } from './app/api/configApi';
import { resolveAuth } from './app/authThunks';
import { useAppDispatch, useAppSelector } from './app/hooks';
import { describeQueryError } from './app/queryError';
import { clearAuth } from './app/slices/authSlice';
import type { OidcConfig } from './auth/auth';
import { signOut } from './auth/auth';
import { fetchPlatformConfig } from './config/platform';
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

interface AuthPhase {
  oidcResolved: boolean;
  oidc?: OidcConfig;
  oidcFallbackReason?: string;
  status: 'resolving' | 'signed-out' | 'authenticated';
  token?: string;
  tokenError?: string;
}

interface ConfigQueryPhase {
  isLoading: boolean;
  isUninitialized: boolean;
  error?: unknown;
  data?: TenantConfigView;
}

// A 401 means two completely different things depending on whether a token was
// held. With no token the caller is simply signed out. With a token, the
// identity provider authenticated them and the API still said no — so the
// identity is not enrolled, and telling them to sign in is a loop (#1167).
function loadStateFromConfigQuery(token: string, configQuery: ConfigQueryPhase): LoadState {
  if (configQuery.isLoading || configQuery.isUninitialized) {
    return { status: 'loading' };
  }
  if (configQuery.error !== undefined) {
    const described = describeQueryError(configQuery.error);
    return described.status === 401
      ? { status: 'not-enrolled', token }
      : { status: 'error', message: described.message };
  }
  if (configQuery.data !== undefined) {
    return { status: 'ready', config: configQuery.data, token };
  }
  return { status: 'loading' };
}

function computeLoadState(auth: AuthPhase, configQuery: ConfigQueryPhase): LoadState {
  if (!auth.oidcResolved || auth.status === 'resolving') {
    return { status: 'loading' };
  }
  if (auth.tokenError !== undefined) {
    return { status: 'error', message: auth.tokenError };
  }
  if (auth.status === 'signed-out' || auth.token === undefined) {
    return { status: 'signed-out' };
  }
  return loadStateFromConfigQuery(auth.token, configQuery);
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

function AppContent({
  brand,
  state,
  oidc,
  oidcFallbackReason,
  onChanged,
  onSignOut,
}: {
  brand: string | undefined;
  state: LoadState;
  oidc: OidcConfig | undefined;
  oidcFallbackReason: string | undefined;
  onChanged: () => void;
  onSignOut: () => void;
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
          onChanged={onChanged}
          onSignOut={onSignOut}
        />
      );
  }
}

export function App(): React.ReactElement {
  const brand = useBrand();
  const dispatch = useAppDispatch();
  const auth = useAppSelector((s) => s.auth);

  // Resolves the OIDC config from platform discovery (GET /v1/platform), then
  // the bearer token (an OIDC callback exchange, a token held this session,
  // or the dev-token fallback) — see app/authThunks.ts.
  React.useEffect(() => {
    void dispatch(resolveAuth());
  }, [dispatch]);

  // Loads the tenant config once a token is known. `refetch` is also the
  // refresh a write surface (register/deploy) triggers on completion, so a
  // newly registered env or a settled deploy's status shows up here too —
  // via `invalidatesTags: ['Config']` on those mutations.
  const configQuery = useGetConfigQuery(auth.token ?? '', { skip: auth.token === undefined });

  const state = computeLoadState(auth, configQuery);

  // IconTooltip (used by the theme toggle and, once panels adopt it, elsewhere)
  // requires a TooltipProvider ancestor, same as the desktop's App.tsx.
  return (
    <TooltipProvider>
      <AppContent
        brand={brand}
        state={state}
        oidc={auth.oidc}
        oidcFallbackReason={auth.oidcFallbackReason}
        onChanged={() => {
          void configQuery.refetch();
        }}
        onSignOut={() => {
          signOut();
          dispatch(clearAuth());
        }}
      />
    </TooltipProvider>
  );
}

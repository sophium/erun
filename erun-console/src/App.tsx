import { type TenantConfigView, TooltipProvider } from 'erun-kit';
import * as React from 'react';

import { useGetConfigQuery } from './app/api/configApi';
import { resolveAuth } from './app/authThunks';
import { useAppDispatch, useAppSelector } from './app/hooks';
import { describeQueryError } from './app/queryError';
import { clearAuth } from './app/slices/authSlice';
import type { OidcConfig } from './auth/auth';
import { beginLogin, signOut } from './auth/auth';
import { fetchPlatformConfig } from './config/platform';
import { AcceptInvitePage } from './identity/AcceptInvitePage';
import { AppShell } from './shell/AppShell';
import { LandingScreen } from './shell/LandingScreen';
import { ErrorScreen, LoadingScreen, NotEnrolledScreen } from './shell/PreShellScreens';
import { beginTenantSwitch, consumeTenantSwitchIntent } from './shell/tenantSwitch';
import type { TenantSwitchMismatch } from './shell/TenantSwitchMismatchBanner';
import { applyTheme, initialTheme } from './shell/theme';

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

interface PlatformInfo {
  brand?: string;
  docsUrl?: string;
  tagline?: string;
  logoUrl?: string;
}

// usePlatformInfo resolves the instance's white-label surface from discovery
// (GET /v1/platform) and mirrors the brand into the document title — the one
// thing that used to be a hardcoded literal (`erun console`) despite the
// value already being fetched and parsed for OIDC config. Every field is
// optional and left undefined when the backend has it unset (#1327); callers
// fall back to bundled product-level defaults, never a hardcoded instance
// name.
function usePlatformInfo(): PlatformInfo {
  const [info, setInfo] = React.useState<PlatformInfo>({});
  React.useEffect(() => {
    let cancelled = false;
    fetchPlatformConfig()
      .then((platform) => {
        if (cancelled || platform === undefined) {
          return;
        }
        setInfo({
          brand: platform.brand.length > 0 ? platform.brand : undefined,
          docsUrl: platform.docsUrl.length > 0 ? platform.docsUrl : undefined,
          tagline: platform.tagline.length > 0 ? platform.tagline : undefined,
          logoUrl: platform.logoUrl.length > 0 ? platform.logoUrl : undefined,
        });
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);
  React.useEffect(() => {
    document.title = info.brand !== undefined ? `${info.brand} console` : 'ERun console';
  }, [info.brand]);
  return info;
}

// retrySwitchHandler rebuilds the pending-switch intent and relaunches sign-in
// so "try again" repeats the same requested target rather than losing it —
// undefined (no retry offered) when there is nothing to retry against, either
// because there's no live mismatch or no OIDC config to redirect through
// (the dev-token fallback).
function retrySwitchHandler(
  oidc: OidcConfig | undefined,
  switchMismatch: TenantSwitchMismatch | undefined,
): (() => void) | undefined {
  if (oidc === undefined || switchMismatch === undefined) {
    return undefined;
  }
  return () => {
    beginTenantSwitch({
      tenantId: switchMismatch.requestedTenantId,
      name: switchMismatch.requestedName,
    });
    void beginLogin(oidc, 'select_account');
  };
}

function AppContent({
  platform,
  state,
  oidc,
  oidcFallbackReason,
  switchMismatch,
  onChanged,
  onSignOut,
  onDismissSwitchMismatch,
}: {
  platform: PlatformInfo;
  state: LoadState;
  oidc: OidcConfig | undefined;
  oidcFallbackReason: string | undefined;
  switchMismatch: TenantSwitchMismatch | undefined;
  onChanged: () => void;
  onSignOut: () => void;
  onDismissSwitchMismatch: () => void;
}): React.ReactElement {
  const brand = platform.brand;
  switch (state.status) {
    case 'loading':
      return <LoadingScreen brand={brand} />;
    case 'signed-out':
      return (
        <LandingScreen
          brand={brand}
          logoUrl={platform.logoUrl}
          tagline={platform.tagline}
          docsUrl={platform.docsUrl}
          oidc={oidc}
          fallbackReason={oidcFallbackReason}
        />
      );
    case 'not-enrolled':
      return <NotEnrolledScreen brand={brand} token={state.token} onSignOut={onSignOut} />;
    case 'error':
      return <ErrorScreen brand={brand} message={state.message} />;
    case 'ready':
      return (
        <AppShell
          brand={brand}
          token={state.token}
          config={state.config}
          docsUrl={platform.docsUrl}
          oidc={oidc}
          switchMismatch={switchMismatch}
          onRetrySwitch={retrySwitchHandler(oidc, switchMismatch)}
          onDismissSwitchMismatch={onDismissSwitchMismatch}
          onChanged={onChanged}
          onSignOut={onSignOut}
        />
      );
  }
}

// isAcceptInvitePath and acceptInviteToken read the URL directly rather than
// through a router: the console has no client-side routing today, and an
// invite link is the one page that must render before OIDC sign-in ever
// starts — an invitee has no bearer token and no tenant membership yet, so
// forcing them through the normal sign-in flow first would be a dead end.
function isAcceptInvitePath(): boolean {
  return window.location.pathname === '/accept-invite';
}

function acceptInviteToken(): string {
  return new URLSearchParams(window.location.search).get('token') ?? '';
}

export function App(): React.ReactElement {
  const platform = usePlatformInfo();
  const dispatch = useAppDispatch();
  const auth = useAppSelector((s) => s.auth);
  const acceptInvite = isAcceptInvitePath();

  // The `.dark` class only gets applied here, once, so every pre-shell screen
  // (including the signed-out landing page) honors a stored or OS-level dark
  // preference from first paint — not only after AppShell's own toggle (whose
  // useTheme() re-reads the same preference) has mounted post-sign-in.
  React.useEffect(() => {
    applyTheme(initialTheme());
  }, []);

  // Resolves the OIDC config from platform discovery (GET /v1/platform), then
  // the bearer token (an OIDC callback exchange, a token held this session,
  // or the dev-token fallback) — see app/authThunks.ts. Skipped on the
  // accept-invite page: an invitee has no identity to resolve yet.
  React.useEffect(() => {
    if (acceptInvite) {
      return;
    }
    void dispatch(resolveAuth());
  }, [dispatch, acceptInvite]);

  // Loads the tenant config once a token is known. `refetch` is also the
  // refresh a write surface (register/deploy) triggers on completion, so a
  // newly registered env or a settled deploy's status shows up here too —
  // via `invalidatesTags: ['Config']` on those mutations.
  const configQuery = useGetConfigQuery(auth.token ?? '', {
    skip: acceptInvite || auth.token === undefined,
  });

  const state = computeLoadState(auth, configQuery);

  // A pending tenant-switch target (shell/tenantSwitch.ts) is consumed exactly
  // once, the first time the config a fresh sign-in produced is actually
  // known — a ref (not just the effect's dependency) guards that, since
  // `state` is a fresh object every render and consuming twice would be a
  // silent no-op the second time anyway, but only the first matters for
  // deciding whether this sign-in reached its requested target. Declared
  // unconditionally (above the accept-invite early return below) because
  // hooks cannot run conditionally.
  const switchIntentConsumed = React.useRef(false);
  const [switchMismatch, setSwitchMismatch] = React.useState<TenantSwitchMismatch | undefined>(
    undefined,
  );
  React.useEffect(() => {
    if (acceptInvite || state.status !== 'ready' || switchIntentConsumed.current) {
      return;
    }
    switchIntentConsumed.current = true;
    const intent = consumeTenantSwitchIntent();
    if (intent !== undefined && intent.tenantId !== state.config.tenant.tenantId) {
      setSwitchMismatch({
        requestedTenantId: intent.tenantId,
        requestedName: intent.name,
        resolvedName: state.config.tenant.name,
        resolvedType: state.config.tenant.type,
      });
    }
  }, [acceptInvite, state]);

  if (acceptInvite) {
    return <AcceptInvitePage token={acceptInviteToken()} />;
  }

  // IconTooltip (used by the theme toggle and, once panels adopt it, elsewhere)
  // requires a TooltipProvider ancestor, same as the desktop's App.tsx.
  return (
    <TooltipProvider>
      <AppContent
        platform={platform}
        state={state}
        oidc={auth.oidc}
        oidcFallbackReason={auth.oidcFallbackReason}
        switchMismatch={switchMismatch}
        onChanged={() => {
          void configQuery.refetch();
        }}
        onSignOut={() => {
          signOut();
          dispatch(clearAuth());
        }}
        onDismissSwitchMismatch={() => {
          setSwitchMismatch(undefined);
        }}
      />
    </TooltipProvider>
  );
}

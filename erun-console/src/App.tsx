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
import { consoleBrand, consoleBrandLabel } from './shell/brand';
import { LandingScreen } from './shell/LandingScreen';
import {
  ErrorScreen,
  LoadingScreen,
  NotEnrolledScreen,
  ResolutionErrorScreen,
  TenantUnresolvedScreen,
} from './shell/PreShellScreens';
import { beginTenantSwitch, consumeTenantSwitchIntent } from './shell/tenantSwitch';
import type { TenantSwitchMismatch } from './shell/TenantSwitchMismatchBanner';
import { applyTheme, initialTheme } from './shell/theme';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; config: TenantConfigView; token: string }
  | { status: 'signed-out' }
  // Signed in, but the identity belongs to no tenant; signing in again would loop.
  | { status: 'not-enrolled'; token: string }
  // Signed in, but the API could not tell which tenant the token resolves to;
  // the caller may already be enrolled, so enrolment advice is wrong here.
  | { status: 'tenant-unresolved'; token: string; message: string }
  // The API's identity resolution failed internally; no enrolment advice applies.
  | { status: 'resolution-error'; token: string; message: string }
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

// A 401 without a token is signed-out; with one, the API's error code picks the
// card. Unclassified 401s fall back to not-enrolled.
function loadStateFromConfigQuery(token: string, configQuery: ConfigQueryPhase): LoadState {
  if (configQuery.isLoading || configQuery.isUninitialized) {
    return { status: 'loading' };
  }
  if (configQuery.error !== undefined) {
    const described = describeQueryError(configQuery.error);
    if (described.status === 401 && described.code === 'TENANT_UNRESOLVED') {
      return { status: 'tenant-unresolved', token, message: described.message };
    }
    if (described.status === 401 && described.code === 'RESOLUTION_FAILED') {
      return { status: 'resolution-error', token, message: described.message };
    }
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
  // Always a string so the pre-discovery paint shows the same name discovery resolves to.
  brand: string;
  docsUrl?: string;
  tagline?: string;
  logoUrl?: string;
}

// usePlatformInfo resolves the white-label surface from GET /v1/platform and mirrors the brand into the document title.
function usePlatformInfo(): PlatformInfo {
  const [info, setInfo] = React.useState<PlatformInfo>(() => ({
    brand: consoleBrand(undefined),
  }));
  React.useEffect(() => {
    let cancelled = false;
    fetchPlatformConfig()
      .then((platform) => {
        if (cancelled || platform === undefined) {
          return;
        }
        setInfo({
          brand: consoleBrand(platform.brand),
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
    document.title = consoleBrandLabel(info.brand);
  }, [info.brand]);
  return info;
}

// retrySwitchHandler is undefined when there is no mismatch or no OIDC config to redirect through.
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
      return (
        <NotEnrolledScreen brand={brand} token={state.token} oidc={oidc} onSignOut={onSignOut} />
      );
    case 'tenant-unresolved':
      return (
        <TenantUnresolvedScreen
          brand={brand}
          token={state.token}
          message={state.message}
          onSignOut={onSignOut}
        />
      );
    case 'resolution-error':
      return (
        <ResolutionErrorScreen
          brand={brand}
          token={state.token}
          message={state.message}
          onSignOut={onSignOut}
        />
      );
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

// The invite page must render before OIDC sign-in: an invitee has no token or tenant yet.
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

  // Applied here so pre-shell screens honor the dark preference from first paint.
  React.useEffect(() => {
    applyTheme(initialTheme());
  }, []);

  // Skipped on the accept-invite page: an invitee has no identity to resolve yet.
  React.useEffect(() => {
    if (acceptInvite) {
      return;
    }
    void dispatch(resolveAuth());
  }, [dispatch, acceptInvite]);

  const configQuery = useGetConfigQuery(auth.token ?? '', {
    skip: acceptInvite || auth.token === undefined,
  });

  const state = computeLoadState(auth, configQuery);

  // A pending tenant-switch target is consumed exactly once; declared above the
  // accept-invite early return because hooks cannot run conditionally.
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

  // IconTooltip requires a TooltipProvider ancestor.
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
          void (async () => {
            // When signOut redirects to the IdP the page is leaving, so clearAuth is skipped.
            const result = await signOut(auth.oidc);
            if (!result.idpSessionEnded) {
              dispatch(clearAuth());
            }
          })();
        }}
        onDismissSwitchMismatch={() => {
          setSwitchMismatch(undefined);
        }}
      />
    </TooltipProvider>
  );
}

import {
  AlertCircle,
  CheckCircle2,
  Cloud,
  Copy,
  LoaderCircle,
  LogIn,
  LogOut,
  RefreshCw,
  UserCircle2,
} from 'lucide-react';
import * as React from 'react';

import {
  getPrimaryCloudProviderBearerToken,
  loginPrimaryCloudProvider,
  logoutPrimaryCloudProvider,
} from '@/app/cloudProviderThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { sidebarCloudAliases } from '@/components/app/Sidebar.helpers';
import { cloudProviderStatusTone } from '@/components/app/StatusBadge.helpers';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import { CloudProviderCloudflare, type UICloudProviderStatus } from '@/types';

interface CloudAliasRowView {
  provider: UICloudProviderStatus;
  isCloudflare: boolean;
  active: boolean;
  busy: boolean;
  loginBusy: boolean;
  logoutBusy: boolean;
  bearerBusy: boolean;
}

// PrimaryCloudAliasControl renders a login/status row per cloud alias the active
// tenant uses. AWS aliases authenticate via OIDC and can mint a bearer token to
// authenticate the runtime; Cloudflare aliases use a scoped API token with no
// OIDC, so the bearer action is hidden and "Log in" becomes "Verify token".
export function PrimaryCloudAliasControl(): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const tenants = useAppSelector((s) => s.tenants.tenants);
  const cloudProviders = useAppSelector((s) => s.tenants.cloudProviders);
  const selected = useAppSelector((s) => s.selection.selected);
  const dashboardTenant = useAppSelector((s) => s.tenantDashboard.tenant);
  const busyByAlias = useAppSelector((s) => s.sidebar.sidebarCloudAliasBusyByAlias);
  const aliases = sidebarCloudAliases({ tenants, cloudProviders, selected, dashboardTenant });
  if (aliases.length === 0) {
    return null;
  }
  return (
    <div className="mt-3 mr-1 grid gap-1.5">
      {aliases.map((alias) => (
        <CloudAliasRow
          key={alias}
          view={cloudAliasRowView(alias, cloudProviders, busyByAlias)}
          dispatch={dispatch}
        />
      ))}
    </div>
  );
}

function cloudAliasRowView(
  alias: string,
  cloudProviders: UICloudProviderStatus[],
  busyByAlias: Record<string, string>,
): CloudAliasRowView {
  const provider = cloudProviders.find((candidate) => candidate.alias === alias) ?? {
    alias,
    provider: '',
    status: 'unknown',
  };
  const action = busyByAlias[alias] ?? '';
  const busy = action !== '';
  return {
    provider,
    isCloudflare: provider.provider.trim().toLowerCase() === CloudProviderCloudflare,
    active: provider.status.trim() === 'active',
    busy,
    loginBusy: action === 'login',
    logoutBusy: action === 'logout',
    bearerBusy: action === 'bearer',
  };
}

function CloudAliasRow({
  view,
  dispatch,
}: {
  view: CloudAliasRowView;
  dispatch: ReturnType<typeof useAppDispatch>;
}): React.ReactElement {
  const triggerTone = cloudProviderStatusTone(view.provider.status);
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="flex min-h-10 min-w-0 items-center gap-2 rounded-md border border-sidebar-border bg-background/88 px-3 py-2 text-left text-sm text-foreground shadow-sm hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
          aria-label={`${view.provider.alias} cloud status`}
        >
          <CloudAliasTriggerIcon tone={triggerTone} busy={view.busy} />
          <span className="min-w-0 flex-1 truncate">{cloudProviderIdentity(view.provider)}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[min(360px,calc(var(--sidebar-width)-24px))] p-2"
        side="top"
        align="start"
      >
        <CloudAliasPopoverBody view={view} dispatch={dispatch} />
      </PopoverContent>
    </Popover>
  );
}

function CloudAliasPopoverBody({
  view,
  dispatch,
}: {
  view: CloudAliasRowView;
  dispatch: ReturnType<typeof useAppDispatch>;
}): React.ReactElement {
  return (
    <div className="grid gap-1">
      <CloudAliasPopoverRow
        icon={<UserCircle2 />}
        label={cloudProviderIdentity(view.provider)}
        muted
      />
      <CloudAliasPopoverRow icon={<Cloud />} label={view.provider.alias} muted />
      <div className="my-1 border-t border-border" />
      <CloudAliasStatus provider={view.provider} />
      {view.active ? (
        <CloudAliasActiveActions view={view} dispatch={dispatch} />
      ) : (
        <CloudAliasLoginAction view={view} dispatch={dispatch} />
      )}
    </div>
  );
}

function CloudAliasActiveActions({
  view,
  dispatch,
}: {
  view: CloudAliasRowView;
  dispatch: ReturnType<typeof useAppDispatch>;
}): React.ReactElement {
  return (
    <>
      {!view.isCloudflare && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="justify-start"
          disabled={view.busy}
          onClick={() => void dispatch(getPrimaryCloudProviderBearerToken(view.provider.alias))}
        >
          {view.bearerBusy ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Copy aria-hidden="true" />
          )}
          {view.bearerBusy ? 'Copying token...' : 'Get bearer token'}
        </Button>
      )}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="justify-start"
        disabled={view.busy}
        onClick={() => void dispatch(logoutPrimaryCloudProvider(view.provider.alias))}
      >
        {view.logoutBusy ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <LogOut aria-hidden="true" />
        )}
        {cloudAliasLogoutLabel(view)}
      </Button>
    </>
  );
}

function cloudAliasLogoutLabel(view: CloudAliasRowView): string {
  if (view.isCloudflare) {
    return view.logoutBusy ? 'Removing token...' : 'Remove token';
  }
  return view.logoutBusy ? 'Logging out...' : 'Log out';
}

function CloudAliasLoginAction({
  view,
  dispatch,
}: {
  view: CloudAliasRowView;
  dispatch: ReturnType<typeof useAppDispatch>;
}): React.ReactElement {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="justify-start"
      disabled={view.busy}
      onClick={() => void dispatch(loginPrimaryCloudProvider(view.provider.alias))}
    >
      {view.loginBusy ? (
        <LoaderCircle className="animate-spin" aria-hidden="true" />
      ) : view.isCloudflare ? (
        <RefreshCw aria-hidden="true" />
      ) : (
        <LogIn aria-hidden="true" />
      )}
      {cloudAliasLoginLabel(view)}
    </Button>
  );
}

function cloudAliasLoginLabel(view: CloudAliasRowView): string {
  if (view.isCloudflare) {
    return view.loginBusy ? 'Verifying...' : 'Verify token';
  }
  return view.loginBusy ? 'Logging in...' : 'Log in';
}

function CloudAliasPopoverRow({
  icon,
  label,
  muted,
}: {
  icon: React.ReactElement<{ className?: string; 'aria-hidden'?: boolean }>;
  label: string;
  muted?: boolean;
}): React.ReactElement {
  return (
    <div
      className={cn(
        'flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm',
        muted && 'text-muted-foreground',
      )}
    >
      {React.cloneElement(icon, { className: 'size-4 shrink-0', 'aria-hidden': true })}
      <span className="truncate">{label}</span>
    </div>
  );
}

function CloudAliasStatus({ provider }: { provider: UICloudProviderStatus }): React.ReactElement {
  const tone = cloudProviderStatusTone(provider.status);
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm">
      <CloudAliasStatusIcon tone={tone} />
      <span className="min-w-0 flex-1 truncate">{statusLabel(provider.status)}</span>
    </div>
  );
}

function CloudAliasTriggerIcon({
  tone,
  busy,
}: {
  tone: ReturnType<typeof cloudProviderStatusTone>;
  busy: boolean;
}): React.ReactElement {
  if (busy) {
    return (
      <LoaderCircle
        className="size-4 shrink-0 animate-spin text-muted-foreground"
        aria-hidden="true"
      />
    );
  }
  if (tone === 'success') {
    return (
      <CheckCircle2
        className="size-4 shrink-0 text-green-700 dark:text-green-400"
        aria-hidden="true"
      />
    );
  }
  if (tone === 'destructive') {
    return <AlertCircle className="size-4 shrink-0 text-destructive" aria-hidden="true" />;
  }
  return <UserCircle2 className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
}

function CloudAliasStatusIcon({
  tone,
}: {
  tone: ReturnType<typeof cloudProviderStatusTone>;
}): React.ReactElement {
  if (tone === 'success') {
    return (
      <CheckCircle2
        className="size-4 shrink-0 text-green-700 dark:text-green-400"
        aria-hidden="true"
      />
    );
  }
  if (tone === 'destructive') {
    return <AlertCircle className="size-4 shrink-0 text-destructive" aria-hidden="true" />;
  }
  return <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
}

function cloudProviderIdentity(provider: UICloudProviderStatus): string {
  const username = provider.username?.trim() ?? '';
  return username !== '' ? username : provider.alias;
}

function statusLabel(status: string): string {
  switch (status.trim()) {
    case 'expired':
      return 'Login expired';
    case 'not_configured':
      return 'Not configured';
    case 'active':
      return 'Connected';
    default:
      return 'Status unknown';
  }
}

import { Button, cloudProviderStatusTone, StatusBadge } from 'erun-kit';
import { CheckCircle2, LoaderCircle, LogIn, LogOut, Play, Power, UserCircle2 } from 'lucide-react';
import * as React from 'react';

import { dialogErrorClassName, statusLabel } from '@/components/app/GlobalConfigDialog.helpers';

export function DialogError({ error }: { error: string }): React.ReactElement | null {
  return error ? (
    <div className={dialogErrorClassName} role="alert">
      {error}
    </div>
  ) : null;
}

export function CloudStatusBadge({ status }: { status: string }): React.ReactElement {
  const normalized = status.trim() || 'unknown';
  return <StatusBadge tone={cloudProviderStatusTone(normalized)} label={statusLabel(normalized)} />;
}

export function CloudAliasAction({
  status,
  busy,
  loading,
  logoutLoading,
  switchLoading,
  onLogin,
  onLogout,
  onSwitch,
  // Cloudflare has no OIDC/SSO identity to swap -- its "login" only re-verifies
  // a static token -- so the caller omits onSwitch to hide the action.
  canSwitchIdentity,
  loginLabel,
  loadingLabel,
}: {
  status: string;
  busy: boolean;
  loading: boolean;
  logoutLoading: boolean;
  switchLoading: boolean;
  onLogin: () => void;
  onLogout: () => void;
  onSwitch: () => void;
  canSwitchIdentity: boolean;
  // Cloudflare re-verifies a stored token instead of running a browser SSO, so
  // it relabels this action ("Verify token" / "Verifying...").
  loginLabel?: string;
  loadingLabel?: string;
}): React.ReactElement {
  if (status.trim() === 'active') {
    return (
      <CloudAliasActiveActions
        busy={busy}
        logoutLoading={logoutLoading}
        switchLoading={switchLoading}
        canSwitchIdentity={canSwitchIdentity}
        onLogout={onLogout}
        onSwitch={onSwitch}
      />
    );
  }
  const idleLabel = loginLabel ?? 'Login';
  const busyLabel = loadingLabel ?? 'Logging in...';
  return (
    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onLogin}>
      {loading ? (
        <LoaderCircle className="animate-spin" aria-hidden="true" />
      ) : (
        <LogIn aria-hidden="true" />
      )}
      {loading ? busyLabel : idleLabel}
    </Button>
  );
}

// CloudAliasActiveActions is the "Connected" row's own action set -- split
// out of CloudAliasAction so that function stays under the cyclop threshold;
// the rendered result is unchanged.
function CloudAliasActiveActions({
  busy,
  logoutLoading,
  switchLoading,
  canSwitchIdentity,
  onLogout,
  onSwitch,
}: {
  busy: boolean;
  logoutLoading: boolean;
  switchLoading: boolean;
  canSwitchIdentity: boolean;
  onLogout: () => void;
  onSwitch: () => void;
}): React.ReactElement {
  return (
    <div className="flex flex-wrap items-center justify-end gap-1">
      <span
        className="inline-flex items-center gap-1.5 px-1 text-xs font-medium text-green-700 dark:text-green-400"
        aria-label="Connected"
      >
        <CheckCircle2 className="size-4" aria-hidden="true" />
        Connected
      </span>
      {canSwitchIdentity && (
        <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={onSwitch}>
          {switchLoading ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <UserCircle2 aria-hidden="true" />
          )}
          {switchLoading ? 'Switching...' : 'Sign in as someone else'}
        </Button>
      )}
      <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={onLogout}>
        {logoutLoading ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <LogOut aria-hidden="true" />
        )}
        {logoutLoading ? 'Logging out...' : 'Log out'}
      </Button>
    </div>
  );
}

export function CloudContextAction({
  status,
  busy,
  loading,
  onStart,
  onStop,
}: {
  status: string;
  busy: boolean;
  loading: boolean;
  onStart: () => void;
  onStop: () => void;
}): React.ReactElement {
  if (status.trim() === 'running') {
    return (
      <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onStop}>
        {loading ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Power aria-hidden="true" />
        )}
        {loading ? 'Stopping...' : 'Stop'}
      </Button>
    );
  }
  return (
    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onStart}>
      {loading ? (
        <LoaderCircle className="animate-spin" aria-hidden="true" />
      ) : (
        <Play aria-hidden="true" />
      )}
      {loading ? 'Starting...' : 'Start'}
    </Button>
  );
}

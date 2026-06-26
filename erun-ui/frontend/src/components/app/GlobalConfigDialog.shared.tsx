import { CheckCircle2, LoaderCircle, LogIn, Play, Power } from 'lucide-react';
import * as React from 'react';

import { dialogErrorClassName, statusLabel } from '@/components/app/GlobalConfigDialog.helpers';
import { StatusBadge } from '@/components/app/StatusBadge';
import { cloudProviderStatusTone } from '@/components/app/StatusBadge.helpers';
import { Button } from '@/components/ui/button';

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
  onLogin,
  loginLabel,
  loadingLabel,
}: {
  status: string;
  busy: boolean;
  loading: boolean;
  onLogin: () => void;
  // loginLabel / loadingLabel let a provider type relabel the action without a
  // second component: Cloudflare re-verifies a stored token rather than running
  // a browser SSO, so its button reads "Verify token" / "Verifying...".
  loginLabel?: string;
  loadingLabel?: string;
}): React.ReactElement {
  if (status.trim() === 'active') {
    return (
      <div
        className="inline-flex items-center gap-1.5 px-1 text-xs font-medium text-green-700 dark:text-green-400"
        aria-label="Connected"
      >
        <CheckCircle2 className="size-4" aria-hidden="true" />
        Connected
      </div>
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

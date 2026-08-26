import { Button, cn, EmptyState } from 'erun-kit';
import {
  AlertTriangle,
  Check,
  Copy,
  ExternalLink,
  Globe,
  LoaderCircle,
  Lock,
  Plus,
  RefreshCw,
  ShieldOff,
  Trash2,
  Unlock,
} from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import {
  cancelUnexposeConfirm,
  refreshManageExposures,
  startUnexposeConfirm,
  submitExposeService,
  submitUnexposeEnvironment,
  updateExposeForm,
} from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { TextField } from '@/components/app/ManageDialog.fields';

import { BrowserOpenURL, ClipboardSetText } from '../../../wailsjs/runtime/runtime';

type ManageDialog = AppState['manageDialog'];

// ExposuresSection is the Ports tab's public-exposure surface (issue #1351):
// it lists an environment's active exposures and their public hostnames,
// exposes a new service, and removes the environment's public DNS record
// behind a two-step confirm. Every reachable state is designed explicitly —
// loading, three distinct empty states (not applicable / restricted /
// nothing yet), populated, failed, and the in-flight create/remove states —
// per erun-ui/AGENTS.md "Degrade by permission" and "Keep the three empty
// states distinct".
export function ExposuresSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const exposures = dialog.exposures;
  const loading = dialog.exposuresLoading;
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Public access
        </div>
      </div>
      {loading ? (
        <ExposuresLoadingSkeleton />
      ) : (
        <ExposuresBody dialog={dialog} exposures={exposures} />
      )}
    </div>
  );
}

function ExposuresLoadingSkeleton(): React.ReactElement {
  return (
    <div className="grid gap-1.5" role="status" aria-label="Loading public addresses">
      <div className="h-9 animate-pulse rounded-[var(--radius)] bg-muted/50" />
      <div className="h-9 w-2/3 animate-pulse rounded-[var(--radius)] bg-muted/50" />
    </div>
  );
}

function ExposuresBody({
  dialog,
  exposures,
}: {
  dialog: ManageDialog;
  exposures: ManageDialog['exposures'];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  if (!exposures.configured) {
    return (
      <EmptyState
        icon={<Globe aria-hidden="true" />}
        heading="Not available for this environment"
        body="This environment's project isn't set up for hosted deployment, so it has no public address to show."
      />
    );
  }
  if (exposures.restricted) {
    return (
      <EmptyState
        icon={<ShieldOff aria-hidden="true" />}
        heading="You may not have access to see this"
        body="Your Kubernetes credentials for this environment don't allow listing its public addresses."
      />
    );
  }
  if (exposures.error) {
    return (
      <div role="alert" className="grid gap-2">
        <EmptyState
          icon={<AlertTriangle aria-hidden="true" />}
          heading="Couldn't load public addresses"
          body={exposures.error}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="justify-self-start"
          onClick={() => void dispatch(refreshManageExposures())}
        >
          <RefreshCw aria-hidden="true" />
          Try again
        </Button>
      </div>
    );
  }
  return (
    <div className="grid gap-3">
      {exposures.services.length === 0 ? (
        <EmptyState
          heading="Nothing exposed yet"
          body="Expose a service below to give it a public address."
        />
      ) : (
        <ExposedServiceList services={exposures.services} />
      )}
      <ExposeServiceForm dialog={dialog} />
      {exposures.services.length > 0 && <UnexposeSection dialog={dialog} />}
    </div>
  );
}

function ExposedServiceList({
  services,
}: {
  services: ManageDialog['exposures']['services'];
}): React.ReactElement {
  return (
    <div className="overflow-hidden rounded-[var(--radius)] border border-border bg-muted/35 text-xs leading-[1.3]">
      {services.map((service) => (
        <ExposedServiceRow key={service.service} service={service} />
      ))}
    </div>
  );
}

function ExposedServiceRow({
  service,
}: {
  service: ManageDialog['exposures']['services'][number];
}): React.ReactElement {
  const url = `${service.scheme}://${service.hostname}`;
  const [copied, setCopied] = React.useState(false);
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 border-b border-border px-3 py-2 last:border-b-0">
      <div className="min-w-0">
        <div className="font-medium text-foreground">{service.service}</div>
        <div className="flex items-center gap-1 text-muted-foreground [overflow-wrap:anywhere]">
          {service.scheme === 'https' ? (
            <Lock className="size-3 shrink-0" aria-hidden="true" />
          ) : (
            <Unlock
              className="size-3 shrink-0 text-amber-600 dark:text-amber-400"
              aria-hidden="true"
            />
          )}
          <span>{url}</span>
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 px-2"
        aria-label={`Copy the address for ${service.service}`}
        onClick={() => {
          void ClipboardSetText(url).then(() => {
            setCopied(true);
            window.setTimeout(() => {
              setCopied(false);
            }, 1400);
          });
        }}
      >
        {copied ? (
          <Check className="text-green-600 dark:text-green-500" aria-hidden="true" />
        ) : (
          <Copy aria-hidden="true" />
        )}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 px-2"
        aria-label={`Open the address for ${service.service}`}
        onClick={() => {
          BrowserOpenURL(url);
        }}
      >
        <ExternalLink aria-hidden="true" />
      </Button>
    </div>
  );
}

function ExposeServiceForm({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const form = dialog.exposeForm;
  const busy = dialog.exposeBusy;
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-dashed border-border p-3">
      <div className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_88px] gap-2">
        <TextField
          id="expose-service-name"
          label="Service name"
          value={form.service}
          disabled={busy}
          placeholder="api"
          helper="Becomes part of the public address."
          onChange={(service) => {
            dispatch(updateExposeForm({ service }));
          }}
        />
        <TextField
          id="expose-target-ip"
          label="Target IP"
          value={form.targetIP}
          disabled={busy}
          placeholder="203.0.113.10"
          helper="The address traffic to this environment already reaches."
          onChange={(targetIP) => {
            dispatch(updateExposeForm({ targetIP }));
          }}
        />
        <TextField
          id="expose-port"
          label="Port"
          value={form.port}
          disabled={busy}
          inputMode="numeric"
          placeholder="80"
          onChange={(port) => {
            dispatch(updateExposeForm({ port }));
          }}
        />
      </div>
      {dialog.exposeError && (
        <div role="alert" className="text-[13px] leading-[1.35] text-destructive">
          {dialog.exposeError}
        </div>
      )}
      <Button
        type="button"
        size="sm"
        className="justify-self-start"
        disabled={busy || !form.service.trim() || !form.targetIP.trim()}
        onClick={() => void dispatch(submitExposeService())}
      >
        {busy ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Plus aria-hidden="true" />
        )}
        {busy ? 'Exposing...' : 'Expose a service'}
      </Button>
    </div>
  );
}

// UnexposeSection removes the environment's public DNS record -- the one
// primitive that exists for taking exposure down, and it covers every
// service listed above at once (see UnexposeEnvironment in exposure_app.go).
// The two-step shape mirrors the Delete tab: a named warning first, a
// separate explicit confirm action second.
function UnexposeSection({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  if (!dialog.unexposeConfirming) {
    return (
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="justify-self-start text-muted-foreground"
        onClick={() => {
          dispatch(startUnexposeConfirm());
        }}
      >
        <Trash2 aria-hidden="true" />
        Remove public access
      </Button>
    );
  }
  const busy = dialog.unexposeBusy;
  const count = dialog.exposures.services.length;
  return (
    <div
      className={cn(
        'grid gap-[9px] rounded-[var(--radius)] border px-[11px] py-2.5 text-[13px] leading-[1.35]',
        'border-[color-mix(in_oklch,var(--destructive)_30%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_7%,transparent)]',
      )}
    >
      <div className="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-[9px]">
        <AlertTriangle className="mt-px size-[17px] text-destructive" aria-hidden="true" />
        <span>
          This removes the public address for every service exposed here ({count}). They will stop
          resolving until re-exposed.
        </span>
      </div>
      {dialog.unexposeError && (
        <div role="alert" className="text-destructive">
          {dialog.unexposeError}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={busy}
          onClick={() => {
            dispatch(cancelUnexposeConfirm());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          disabled={busy}
          onClick={() => void dispatch(submitUnexposeEnvironment())}
        >
          {busy ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Trash2 aria-hidden="true" />
          )}
          {busy ? 'Removing...' : 'Confirm remove'}
        </Button>
      </div>
    </div>
  );
}

import { Button, cn, EmptyState, SelectField } from 'erun-kit';
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

import { openPlatformBlockDocs } from '@/app/documentationThunks';
import { useAppDispatch } from '@/app/hooks';
import {
  cancelUnexposeConfirm,
  refreshManageExposures,
  selectExposeService,
  startUnexposeConfirm,
  submitExposeService,
  submitUnexposeEnvironment,
  updateExposeForm,
} from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';
import { TextField } from '@/components/app/ManageDialog.fields';
import type { UIEnvironmentService } from '@/types';

import { BrowserOpenURL, ClipboardSetText } from '../../../wailsjs/runtime/runtime';

type ManageDialog = AppState['manageDialog'];

// ExposuresSection is the Ports tab's public-exposure surface: it discovers
// the environment's actual Services (issue #1906) instead of asking the
// operator to already know a name, lists the ones already exposed with their
// real address, offers a picker over the rest, and removes the environment's
// public DNS record behind a two-step confirm. Every reachable state is
// designed explicitly -- loading, four distinct empty states (host
// environment type / project not configured for exposure, each named and the
// fixable one carrying its recovery / restricted / nothing yet), populated,
// failed, and the in-flight create/remove states -- per erun-ui/AGENTS.md
// "Degrade by permission" and "Keep the three empty states distinct".
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
    <div className="grid gap-1.5" role="status" aria-label="Loading this environment's Services">
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
    if (exposures.notConfiguredReason === 'host-environment') {
      return (
        <EmptyState
          icon={<Globe aria-hidden="true" />}
          heading="Not available for this environment type"
          body="Host environments have no pod and no cluster, so there is no public address to show or create here."
        />
      );
    }
    return (
      <EmptyState
        icon={<Globe aria-hidden="true" />}
        heading="Not available for this environment"
        body="This environment's project isn't set up for hosted deployment yet -- it needs a platform: block with a base domain in .erun/config.yaml before it can have a public address."
        action={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              dispatch(openPlatformBlockDocs());
            }}
          >
            <ExternalLink aria-hidden="true" />
            View the platform: block reference
          </Button>
        }
      />
    );
  }
  if (exposures.restricted) {
    return (
      <EmptyState
        icon={<ShieldOff aria-hidden="true" />}
        heading="You may not have access to see this"
        body="Your Kubernetes credentials for this environment don't allow listing its Services."
      />
    );
  }
  if (exposures.error) {
    return (
      <div role="alert" className="grid gap-2">
        <EmptyState
          icon={<AlertTriangle aria-hidden="true" />}
          heading="Couldn't load this environment's Services"
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
  const exposed = exposures.services.filter((service) => service.exposed);
  const notExposed = exposures.services.filter((service) => !service.exposed);
  return (
    <div className="grid gap-3">
      {exposed.length === 0 ? (
        <EmptyState
          heading="Nothing exposed yet"
          body="Pick a Service below to give it a public address."
        />
      ) : (
        <ExposedServiceList services={exposed} />
      )}
      {exposures.services.length === 0 ? (
        <EmptyState
          heading="No Services in this environment yet"
          body="Deploy something first -- there is nothing here to expose."
        />
      ) : (
        <ExposeServiceForm dialog={dialog} candidates={notExposed} />
      )}
      {exposed.length > 0 && <UnexposeSection dialog={dialog} />}
    </div>
  );
}

function ExposedServiceList({
  services,
}: {
  services: UIEnvironmentService[];
}): React.ReactElement {
  return (
    <div className="overflow-hidden rounded-[var(--radius)] border border-border bg-muted/35 text-xs leading-[1.3]">
      {services.map((service) => (
        <ExposedServiceRow key={service.name} service={service} />
      ))}
    </div>
  );
}

function ExposedServiceRow({ service }: { service: UIEnvironmentService }): React.ReactElement {
  const url = `${service.scheme ?? ''}://${service.hostname ?? ''}`;
  const [copied, setCopied] = React.useState(false);
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 border-b border-border px-3 py-2 last:border-b-0">
      <div className="min-w-0">
        <div className="font-medium text-foreground">{service.name}</div>
        <div className="flex items-center gap-1 text-muted-foreground [overflow-wrap:anywhere]">
          {service.scheme === 'https' ? (
            <Lock className="size-3 shrink-0" aria-hidden="true" />
          ) : (
            <Unlock
              className="size-3 shrink-0 text-amber-700 dark:text-amber-400"
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
        aria-label={`Copy the address for ${service.name}`}
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
          <Check className="text-green-700 dark:text-green-400" aria-hidden="true" />
        ) : (
          <Copy aria-hidden="true" />
        )}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 px-2"
        aria-label={`Open the address for ${service.name}`}
        onClick={() => {
          BrowserOpenURL(url);
        }}
      >
        <ExternalLink aria-hidden="true" />
      </Button>
    </div>
  );
}

function formatPorts(service: UIEnvironmentService): string {
  if (service.ports.length === 0) {
    return 'no ports';
  }
  return service.ports
    .map((port) => (port.name ? `${port.name}:${String(port.port)}` : String(port.port)))
    .join(', ');
}

function serviceOptionLabel(service: UIEnvironmentService): string {
  const ports = formatPorts(service);
  if (!service.exposableLabel) {
    return `${service.name} (${ports}) -- not exposable yet`;
  }
  return `${service.name} (${ports})`;
}

function ExposeServiceForm({
  dialog,
  candidates,
}: {
  dialog: ManageDialog;
  candidates: UIEnvironmentService[];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const form = dialog.exposeForm;
  const busy = dialog.exposeBusy;
  const selected = candidates.find((service) => service.name === form.selectedService);
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-dashed border-border p-3">
      <SelectField
        id="expose-service-picker"
        label="Service"
        value={form.selectedService}
        disabled={busy}
        placeholder="Pick a Service to expose"
        options={candidates.map((service) => ({
          value: service.name,
          label: serviceOptionLabel(service),
          disabled: !service.exposableLabel,
        }))}
        helper="A Service already running in this environment -- not a name you have to already know."
        onChange={(value) => {
          void dispatch(selectExposeService(value));
        }}
      />
      {selected && !selected.exposableLabel && <NotExposableNotice service={selected} />}
      {selected?.exposableLabel && <ExposeServiceDetails dialog={dialog} selected={selected} />}
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
        {busy ? 'Exposing...' : 'Expose this service'}
      </Button>
    </div>
  );
}

function NotExposableNotice({ service }: { service: UIEnvironmentService }): React.ReactElement {
  return (
    <div role="status" className="text-[13px] leading-[1.35] text-muted-foreground">
      <code>{service.name}</code> doesn&apos;t carry this tenant&apos;s naming prefix, so `erun
      expose` has no Service to route to yet.
    </div>
  );
}

// ExposeServiceDetails is the Target IP + port + resolved-hostname preview
// shown once a picked Service can actually be exposed -- split out of
// ExposeServiceForm to keep that component's own complexity down.
function ExposeServiceDetails({
  dialog,
  selected,
}: {
  dialog: ManageDialog;
  selected: UIEnvironmentService;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const form = dialog.exposeForm;
  const busy = dialog.exposeBusy;
  const ports = selected.ports;
  return (
    <>
      <div className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_88px] gap-2">
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
        <ExposePortField dialog={dialog} ports={ports} />
      </div>
      <ExposePreview dialog={dialog} />
    </>
  );
}

// ExposePortField picks among a Service's ports with a select once it has
// more than one, and falls back to a plain numeric field (the default-port
// server-side fallback still applies) for the common single-port case.
function ExposePortField({
  dialog,
  ports,
}: {
  dialog: ManageDialog;
  ports: UIEnvironmentService['ports'];
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const form = dialog.exposeForm;
  const busy = dialog.exposeBusy;
  if (ports.length > 1) {
    return (
      <SelectField
        id="expose-port"
        label="Port"
        value={form.port || String(ports[0]?.port ?? '')}
        disabled={busy}
        options={ports.map((port) => ({
          value: String(port.port),
          label: port.name ? `${port.name} (${String(port.port)})` : String(port.port),
        }))}
        onChange={(port) => {
          dispatch(updateExposeForm({ port }));
        }}
      />
    );
  }
  return (
    <TextField
      id="expose-port"
      label="Port"
      value={form.port}
      disabled={busy}
      inputMode="numeric"
      placeholder={ports[0] ? String(ports[0].port) : '80'}
      onChange={(port) => {
        dispatch(updateExposeForm({ port }));
      }}
    />
  );
}

// ExposePreview shows the hostname a pick will get before it is committed
// (issue #1906) -- the resolved plan, not a guess: the same primitive a real
// expose runs, forced into a dry run.
function ExposePreview({ dialog }: { dialog: ManageDialog }): React.ReactElement | null {
  const form = dialog.exposeForm;
  if (form.previewLoading) {
    return (
      <div
        role="status"
        className="flex items-center gap-1.5 text-[13px] leading-[1.35] text-muted-foreground"
      >
        <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
        Resolving the address this will get...
      </div>
    );
  }
  if (form.previewError) {
    return (
      <div role="status" className="text-[13px] leading-[1.35] text-muted-foreground">
        {form.previewError}
      </div>
    );
  }
  const preview = form.preview;
  if (!preview) {
    return null;
  }
  const url = `${preview.scheme}://${preview.hostname}`;
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex items-center gap-1.5 text-[13px] leading-[1.35] text-foreground"
    >
      {preview.tlsEnabled ? (
        <Lock className="size-3.5 shrink-0" aria-hidden="true" />
      ) : (
        <Unlock
          className="size-3.5 shrink-0 text-amber-700 dark:text-amber-400"
          aria-hidden="true"
        />
      )}
      <span>
        Will be reachable at <code>{url}</code>
        {!preview.tlsEnabled && preview.tlsDisabledReason && ` (${preview.tlsDisabledReason})`}
      </span>
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
  const count = dialog.exposures.services.filter((service) => service.exposed).length;
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
